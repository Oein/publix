// Package engine turns a commit into running containers.
//
// The platform keeps exactly one live deployment per project. A deploy
// builds the new generation, proves it healthy on its own private URL,
// moves traffic to it in one atomic step, and only then reaps the old
// generation. Nothing else is kept resident, which is what keeps a host
// running twenty projects from carrying sixty idle containers.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

// Engine executes deployments. It is safe for concurrent use: work for one
// project is serialised, and work across projects is bounded by the
// configured build concurrency.
type Engine struct {
	store  *store.Store
	docker *dockerapi.Client
	logs   *buildlog.Store

	// sem bounds concurrent deploys across all projects, so a webhook storm
	// cannot bring the host down by building ten repositories at once.
	sem chan struct{}

	mu sync.Mutex
	// running maps project ID to the deployment currently being processed,
	// which is what makes a second deploy queue behind the first.
	running map[string]*run
	// locks serialises work per project.
	locks map[string]*sync.Mutex

	// Notify, when set, is called after every deployment state change.
	Notify func(projectID, deploymentID string, status store.DeployStatus)

	// GitAuth supplies credentials for cloning a private repository.
	GitAuth func(repo *store.Repo) (string, error)

	// StatusReporter, when set, reports deploy outcomes back to GitHub.
	StatusReporter StatusReporter
}

// StatusReporter publishes deployment outcomes to an external system.
type StatusReporter interface {
	ReportStatus(ctx context.Context, p *store.Project, d *store.Deployment, targetURL string)
}

// run tracks an in-flight deployment so it can be cancelled.
type run struct {
	deployment string
	cancel     context.CancelFunc
}

// New creates an Engine.
func New(st *store.Store, docker *dockerapi.Client, logs *buildlog.Store) *Engine {
	set := st.Settings()
	n := set.BuildConcurrency
	if n < 1 {
		n = 1
	}
	return &Engine{
		store:   st,
		docker:  docker,
		logs:    logs,
		sem:     make(chan struct{}, n),
		running: map[string]*run{},
		locks:   map[string]*sync.Mutex{},
	}
}

// Options describe one deployment request.
type Options struct {
	// Ref is the git ref to deploy. Empty means the project's branch.
	Ref string
	// Commit pins an exact commit, used by rollback.
	Commit string
	// Trigger records what asked for this deploy: "manual", "push",
	// "rollback", "cron", "api".
	Trigger string
	// RollbackFrom names the deployment being undone, for the record.
	RollbackFrom string
	// Force rebuilds even when a usable image already exists.
	Force bool
	// Message and Author override the commit metadata, used when a webhook
	// already knows them and the checkout is shallow.
	Message string
	Author  string
}

// Deploy queues a deployment and returns as soon as it is recorded, so an
// HTTP handler can respond immediately and the dashboard can follow along
// through the log stream.
func (e *Engine) Deploy(projectID string, opt Options) (*store.Deployment, error) {
	p, ok := e.store.Project(projectID)
	if !ok {
		return nil, &store.NotFoundError{Kind: "project", ID: projectID}
	}
	if p.Paused && opt.Trigger != "manual" && opt.Trigger != "api" {
		return nil, fmt.Errorf("project %s is paused", p.Name)
	}

	ref := opt.Ref
	if ref == "" && p.Repo != nil {
		ref = p.Repo.Branch
	}

	dep := &store.Deployment{
		ID:             store.NewID(),
		Status:         store.StatusQueued,
		Trigger:        firstNonEmpty(opt.Trigger, "manual"),
		Commit:         opt.Commit,
		Branch:         ref,
		Message:        opt.Message,
		Author:         opt.Author,
		RolledBackFrom: opt.RollbackFrom,
		QueuedAt:       time.Now().UTC(),
	}
	if len(dep.Commit) >= 8 {
		dep.Short = dep.Commit[:8]
	}
	if err := e.store.AddDeployment(p.ID, dep); err != nil {
		return nil, err
	}
	e.notify(p.ID, dep.ID, store.StatusQueued)

	go e.process(p.ID, dep.ID, opt)
	return dep, nil
}

// Cancel stops an in-flight deployment.
func (e *Engine) Cancel(projectID, deploymentID string) error {
	e.mu.Lock()
	r, ok := e.running[projectID]
	e.mu.Unlock()
	if !ok || (deploymentID != "" && r.deployment != deploymentID) {
		return fmt.Errorf("no deployment is running for this project")
	}
	r.cancel()
	return nil
}

// Running reports the deployment currently being processed for a project.
func (e *Engine) Running(projectID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.running[projectID]
	if !ok {
		return "", false
	}
	return r.deployment, true
}

// projectLock returns the mutex serialising work for one project.
func (e *Engine) projectLock(projectID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[projectID]
	if !ok {
		l = &sync.Mutex{}
		e.locks[projectID] = l
	}
	return l
}

// process runs one deployment to completion.
func (e *Engine) process(projectID, deploymentID string, opt Options) {
	lock := e.projectLock(projectID)
	lock.Lock()
	defer lock.Unlock()

	// A deploy queued behind another may already be obsolete: if a newer
	// deployment has been queued in the meantime, building this one would
	// waste a build slot on output nobody will ever see.
	if e.superseded(projectID, deploymentID) {
		e.finish(projectID, deploymentID, store.StatusCancelled, errors.New("superseded by a newer deployment"))
		return
	}

	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e.mu.Lock()
	e.running[projectID] = &run{deployment: deploymentID, cancel: cancel}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, projectID)
		e.mu.Unlock()
	}()

	log, err := e.logs.Create(deploymentID)
	if err != nil {
		e.finish(projectID, deploymentID, store.StatusFailed, err)
		return
	}
	defer e.logs.Close(deploymentID)

	now := time.Now().UTC()
	e.store.UpdateDeployment(projectID, deploymentID, func(d *store.Deployment) {
		d.Status = store.StatusBuilding
		d.StartedAt = &now
	})
	e.notify(projectID, deploymentID, store.StatusBuilding)

	err = e.run(ctx, projectID, deploymentID, opt, log)

	switch {
	case err == nil:
		e.finish(projectID, deploymentID, store.StatusLive, nil)
		log.Printf("")
		log.Printf("Deployment is live.")
	case ctx.Err() != nil:
		log.Printf("Deployment cancelled.")
		e.finish(projectID, deploymentID, store.StatusCancelled, err)
	default:
		log.Printf("")
		log.Printf("Deployment failed: %v", err)
		e.finish(projectID, deploymentID, store.StatusFailed, err)
	}
}

// superseded reports whether a newer deployment has been queued for the
// project since this one was.
func (e *Engine) superseded(projectID, deploymentID string) bool {
	p, ok := e.store.Project(projectID)
	if !ok {
		return true
	}
	this, ok := p.Deployment(deploymentID)
	if !ok {
		return true
	}
	for _, d := range p.Deployments {
		if d.Number > this.Number && !d.Status.Terminal() {
			return true
		}
	}
	return false
}

// finish records a terminal state and reports it onwards.
func (e *Engine) finish(projectID, deploymentID string, status store.DeployStatus, err error) {
	now := time.Now().UTC()
	e.store.UpdateDeployment(projectID, deploymentID, func(d *store.Deployment) {
		d.Status = status
		d.FinishedAt = &now
		if err != nil {
			d.Error = err.Error()
		}
	})
	e.notify(projectID, deploymentID, status)

	if e.StatusReporter != nil {
		if p, ok := e.store.Project(projectID); ok {
			if d, ok := p.Deployment(deploymentID); ok {
				set := e.store.Settings()
				target := ""
				if set.PublicURL != "" {
					target = fmt.Sprintf("%s/projects/%s/deployments/%s", set.PublicURL, p.Slug, d.ID)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				go func() {
					defer cancel()
					e.StatusReporter.ReportStatus(ctx, p, d, target)
				}()
			}
		}
	}
}

func (e *Engine) notify(projectID, deploymentID string, status store.DeployStatus) {
	if e.Notify != nil {
		e.Notify(projectID, deploymentID, status)
	}
}

// Context carries everything one deployment needs, assembled once so the
// individual phases do not each have to re-derive it.
type Context struct {
	Project    *store.Project
	Deployment *store.Deployment
	Settings   store.Settings
	Spec       *deployspec.Resolved
	Log        *buildlog.Log

	// Dir is the checkout the deployment is built from.
	Dir string
	// Root is Dir plus the project's RootDir, for monorepos.
	Root string
	// Image is the tag built for this deployment.
	Image string
	// Service is the Traefik service name traffic will be pointed at.
	Service string
	// Services maps compose service names to Traefik service names.
	Services map[string]string
	// URL is the deployment's own address.
	URL string
}

// run executes the phases of a deployment in order.
func (e *Engine) run(ctx context.Context, projectID, deploymentID string, opt Options, log *buildlog.Log) error {
	p, ok := e.store.Project(projectID)
	if !ok {
		return &store.NotFoundError{Kind: "project", ID: projectID}
	}
	dep, ok := p.Deployment(deploymentID)
	if !ok {
		return &store.NotFoundError{Kind: "deployment", ID: deploymentID}
	}

	dc := &Context{
		Project:    p,
		Deployment: dep,
		Settings:   e.store.Settings(),
		Log:        log,
		Services:   map[string]string{},
	}

	log.Printf("publix deploy %s  ·  %s", p.Name, dep.ID)

	if err := e.checkout(ctx, dc, opt); err != nil {
		return err
	}
	if err := e.loadSpec(dc); err != nil {
		return err
	}
	if err := e.prepare(ctx, dc); err != nil {
		return err
	}
	if err := e.build(ctx, dc, opt); err != nil {
		return err
	}
	if err := e.release(ctx, dc); err != nil {
		return err
	}
	if err := e.reap(ctx, dc); err != nil {
		// Reaping is cleanup: the deployment is already live and serving.
		// A failure here is worth reporting but must not fail the deploy.
		log.Printf("warning: cleanup after release did not fully succeed: %v", err)
	}
	return nil
}

// prepare makes sure the host-side prerequisites exist before anything runs.
func (e *Engine) prepare(ctx context.Context, dc *Context) error {
	if _, err := e.docker.EnsureNetwork(ctx, dc.Settings.Network, map[string]string{
		"publix.managed": "true",
	}); err != nil {
		return fmt.Errorf("creating the %q network: %w", dc.Settings.Network, err)
	}
	if _, err := e.resolveVolumes(dc); err != nil {
		return err
	}
	return nil
}

// workDir is where a project's checkout lives.
func (e *Engine) workDir(set store.Settings, p *store.Project) string {
	return filepath.Join(set.WorkDir, p.ID)
}

// Docker exposes the engine's docker client to the API layer.
func (e *Engine) Docker() *dockerapi.Client { return e.docker }

// Logs exposes the engine's build log store.
func (e *Engine) Logs() *buildlog.Store { return e.logs }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ensureDir creates a directory, reporting a useful error if it cannot.
func ensureDir(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return nil
}
