package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// runCronJob runs one scheduled command in a throwaway container built from
// the project's live image, with the same environment and shared volumes the
// application itself has. A job therefore sees exactly what the app sees.
func (e *Engine) runCronJob(ctx context.Context, projectID, deploymentID, jobName string) error {
	p, ok := e.store.Project(projectID)
	if !ok {
		return &store.NotFoundError{Kind: "project", ID: projectID}
	}
	dep, ok := p.Deployment(deploymentID)
	if !ok {
		return &store.NotFoundError{Kind: "deployment", ID: deploymentID}
	}
	if dep.Spec == "" {
		return fmt.Errorf("deployment %s has no recorded configuration", deploymentID)
	}
	sp, err := deployspec.Parse([]byte(dep.Spec))
	if err != nil {
		return err
	}

	var job *deployspec.Cron
	for i := range sp.Cron {
		if sp.Cron[i].Name == jobName {
			job = &sp.Cron[i]
			break
		}
	}
	if job == nil {
		return fmt.Errorf("%s has no scheduled job named %q", p.Name, jobName)
	}
	if dep.Image == "" {
		return fmt.Errorf("deployment %s has no image to run the job from", deploymentID)
	}

	set := e.store.Settings()
	dc := &Context{
		Project:    p,
		Deployment: dep,
		Settings:   set,
		Spec:       &deployspec.Resolved{Spec: sp},
	}
	env, err := e.buildEnv(dc)
	if err != nil {
		return err
	}
	binds, err := e.resolveVolumes(dc)
	if err != nil {
		return err
	}

	timeout := job.Timeout.D()
	if timeout <= 0 {
		timeout = time.Hour
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	meta := traefik.Meta{
		ProjectID:  p.ID,
		Slug:       p.Slug,
		Deployment: dep.ID,
		Image:      dep.Image,
		Commit:     dep.Commit,
		Created:    time.Now().UTC().Format(time.RFC3339),
	}
	hc := &dockerapi.HostConfig{
		NetworkMode: set.Network,
		// A finished job leaves nothing behind; its output is captured
		// before the container is removed.
		RestartPolicy: &dockerapi.RestartPolicy{Name: "no"},
	}
	for _, b := range binds {
		hc.Binds = append(hc.Binds, b.String())
	}

	name := fmt.Sprintf("publix-%s-cron-%s-%d", p.Slug, traefik.Slug(jobName), time.Now().Unix())
	created, err := e.docker.CreateContainer(ctx, name, &dockerapi.CreateConfig{
		Image:      dep.Image,
		Cmd:        job.Command,
		Env:        env,
		Labels:     traefik.CronLabels(meta, jobName),
		HostConfig: hc,
	})
	if err != nil {
		return fmt.Errorf("creating the job container: %w", err)
	}
	// The container is removed on every path, including a timeout, so a
	// failing schedule cannot accumulate dead containers on the host.
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer stop()
		_ = e.docker.RemoveContainer(cleanup, created.ID, true, false)
	}()

	if err := e.docker.StartContainer(ctx, created.ID); err != nil {
		return fmt.Errorf("starting the job container: %w", err)
	}

	code, err := e.docker.WaitContainer(ctx, created.ID)
	if err != nil {
		return fmt.Errorf("waiting for the job: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("scheduled job %q exited with code %d", jobName, code)
	}
	return nil
}

// Scheduler fires a project's cron jobs.
//
// It ticks once a minute on the wall clock rather than on an interval timer,
// so jobs fire at the minute they were scheduled for even if the process
// started at an awkward moment or the host clock was adjusted.
type Scheduler struct {
	engine *Engine
	store  *store.Store

	mu   sync.Mutex
	last map[string]time.Time // job key -> minute it last fired

	// OnError is called when a job fails, so the caller can surface it.
	OnError func(project, job string, err error)
}

// NewScheduler creates a scheduler for the platform's projects.
func NewScheduler(e *Engine, st *store.Store) *Scheduler {
	return &Scheduler{engine: e, store: st, last: map[string]time.Time{}}
}

// Run ticks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		s.tick(ctx, time.Now())
	}
}

// tick fires every job whose schedule matches the current minute.
func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	minute := now.Truncate(time.Minute)

	for _, p := range s.store.Projects() {
		if p.Paused {
			continue
		}
		live := p.LiveDeployment()
		if live == nil || live.Spec == "" {
			continue
		}
		sp, err := deployspec.Parse([]byte(live.Spec))
		if err != nil {
			continue
		}
		for _, job := range sp.Cron {
			sched, err := ParseSchedule(job.Schedule)
			if err != nil {
				continue // validation already rejected this at deploy time
			}
			if !sched.Matches(minute) {
				continue
			}

			key := p.ID + "/" + job.Name
			s.mu.Lock()
			fired := s.last[key].Equal(minute)
			if !fired {
				s.last[key] = minute
			}
			s.mu.Unlock()
			if fired {
				continue // a slow tick must not double-fire a job
			}

			go func(projectID, projectName, jobName, deploymentID string) {
				if err := s.engine.runCronJob(ctx, projectID, deploymentID, jobName); err != nil && s.OnError != nil {
					s.OnError(projectName, jobName, err)
				}
			}(p.ID, p.Name, job.Name, live.ID)
		}
	}
}

// NextRuns reports when each of a project's jobs will next fire, for the
// dashboard.
func NextRuns(sp *deployspec.Spec, from time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, job := range sp.Cron {
		if sched, err := ParseSchedule(job.Schedule); err == nil {
			out[job.Name] = sched.Next(from)
		}
	}
	return out
}
