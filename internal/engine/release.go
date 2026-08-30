package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// release starts the new generation, proves it healthy, and moves traffic
// to it. On any failure before the cutover, the previous deployment is
// still live and untouched — which is the entire reason the phases are
// ordered this way.
func (e *Engine) release(ctx context.Context, dc *Context) error {
	dc.URL = e.deploymentURL(dc)

	e.store.UpdateDeployment(dc.Project.ID, dc.Deployment.ID, func(d *store.Deployment) {
		d.Status = store.StatusDeploying
	})
	e.notify(dc.Project.ID, dc.Deployment.ID, store.StatusDeploying)

	var (
		containers []string
		err        error
	)
	if dc.Spec.Kind == deployspec.KindCompose {
		containers, err = e.startCompose(ctx, dc)
	} else {
		containers, err = e.startContainers(ctx, dc)
	}
	if err != nil {
		e.rollbackNewGeneration(dc)
		return err
	}

	if err := e.waitHealthy(ctx, dc, containers); err != nil {
		// Show what the failing container said. A health-gate failure is
		// almost always explained by the last few lines of its own log,
		// and making the user go find them is a waste of their time.
		e.dumpContainerLogs(ctx, dc, containers)
		e.rollbackNewGeneration(dc)
		return err
	}

	if err := e.cutover(ctx, dc); err != nil {
		e.rollbackNewGeneration(dc)
		return err
	}
	return nil
}

// startContainers creates and starts the replicas for a Dockerfile, static
// or prebuilt-image deployment.
func (e *Engine) startContainers(ctx context.Context, dc *Context) ([]string, error) {
	sp := dc.Spec
	dc.Service = traefik.ServiceName(dc.Project.Slug, dc.Deployment.ID)

	// Recreate frees the old generation's resources before claiming new
	// ones, which is the right trade on a host that cannot hold both.
	if sp.Release.Strategy == deployspec.StrategyRecreate {
		dc.Log.Printf("Stopping the previous deployment (strategy: recreate)")
		if err := e.stopGeneration(ctx, dc.Project.ID, dc.Deployment.ID, 0); err != nil {
			return nil, err
		}
	}

	env, err := e.buildEnv(dc)
	if err != nil {
		return nil, err
	}
	binds, err := e.resolveVolumes(dc)
	if err != nil {
		return nil, err
	}
	for _, b := range binds {
		dc.Log.Printf("Mounting shared volume %q at %s", b.Volume, b.MountPath)
	}

	meta := traefik.Meta{
		ProjectID:  dc.Project.ID,
		Slug:       dc.Project.Slug,
		Deployment: dc.Deployment.ID,
		Port:       sp.Port,
		Image:      dc.Image,
		Commit:     dc.Deployment.Commit,
		Branch:     dc.Deployment.Branch,
		Kind:       string(sp.Kind),
		Created:    time.Now().UTC().Format(time.RFC3339),
	}

	hostCfg, err := e.hostConfig(dc, binds)
	if err != nil {
		return nil, err
	}

	var started []string
	for i := 1; i <= sp.ReplicaCount(); i++ {
		meta.Replica = i
		name := traefik.ContainerName(dc.Project.Slug, dc.Deployment.ID, i)

		labels := traefik.BaseLabels(meta, traefik.RoleApp)
		for k, v := range traefik.RouterLabels(&dc.Settings, sp.Spec, meta) {
			labels[k] = v
		}

		cfg := &dockerapi.CreateConfig{
			Image:      dc.Image,
			Cmd:        sp.Command,
			Env:        env,
			Labels:     labels,
			HostConfig: hostCfg,
			NetworkingConfig: &dockerapi.NetworkingConfig{
				EndpointsConfig: map[string]*dockerapi.EndpointSettings{
					dc.Settings.Network: {Aliases: containerAliases(dc, i)},
				},
			},
		}
		if sp.Port > 0 {
			cfg.ExposedPorts = map[string]struct{}{strconv.Itoa(sp.Port) + "/tcp": {}}
		}

		// A container of this name can survive a crash mid-deploy; remove
		// it rather than fail on a name conflict the user cannot see.
		_ = e.docker.RemoveContainer(ctx, name, true, false)

		created, err := e.docker.CreateContainer(ctx, name, cfg)
		if err != nil {
			return started, fmt.Errorf("creating %s: %w", name, err)
		}
		for _, w := range created.Warnings {
			dc.Log.Printf("warning: %s", w)
		}
		if err := e.docker.StartContainer(ctx, created.ID); err != nil {
			return append(started, created.ID), fmt.Errorf("starting %s: %w", name, err)
		}
		started = append(started, created.ID)
		dc.Log.Printf("Started %s", name)
	}
	return started, nil
}

// containerAliases give a deployment's replicas stable in-network names, so
// one project's containers can reach another's by hostname.
func containerAliases(dc *Context, replica int) []string {
	return []string{
		dc.Project.Slug,
		dc.Project.Slug + "-" + dc.Deployment.ID,
		fmt.Sprintf("%s-%s-%d", dc.Project.Slug, dc.Deployment.ID, replica),
	}
}

// hostConfig translates the spec's runtime settings into docker's host
// configuration.
func (e *Engine) hostConfig(dc *Context, binds []Bind) (*dockerapi.HostConfig, error) {
	sp := dc.Spec
	hc := &dockerapi.HostConfig{
		NetworkMode: dc.Settings.Network,
		// unless-stopped survives a host reboot but respects an operator
		// stopping a container by hand, which `always` does not.
		RestartPolicy: &dockerapi.RestartPolicy{Name: "unless-stopped"},
	}
	for _, b := range binds {
		hc.Binds = append(hc.Binds, b.String())
	}
	if dc.Settings.LogDriver != "" {
		hc.LogConfig = &dockerapi.LogConfig{Type: dc.Settings.LogDriver, Config: dc.Settings.LogOptions}
	}
	if sp.Resources.CPU != "" {
		cpus, err := strconv.ParseFloat(sp.Resources.CPU, 64)
		if err != nil {
			return nil, fmt.Errorf("resources.cpu: %w", err)
		}
		hc.NanoCPUs = int64(cpus * 1e9)
	}
	if sp.Resources.Memory != "" {
		n, err := deployspec.ParseSize(sp.Resources.Memory)
		if err != nil {
			return nil, fmt.Errorf("resources.memory: %w", err)
		}
		hc.Memory = n
	}
	if sp.Resources.MemoryReservation != "" {
		n, err := deployspec.ParseSize(sp.Resources.MemoryReservation)
		if err != nil {
			return nil, fmt.Errorf("resources.memoryReservation: %w", err)
		}
		hc.MemoryReservation = n
	}
	if sp.Resources.PidsLimit > 0 {
		hc.PidsLimit = &sp.Resources.PidsLimit
	}
	return hc, nil
}

// cutover moves production traffic onto the new deployment.
//
// This is the only step that is visible to users, and it is a single atomic
// file write followed by Traefik's own hot reload. Everything before it was
// preparation; everything after it is cleanup.
func (e *Engine) cutover(ctx context.Context, dc *Context) error {
	if err := e.store.Promote(dc.Project.ID, dc.Deployment.ID); err != nil {
		return err
	}
	// Re-read: Promote is what moved the outgoing deployment into the
	// Previous slot, and the routing rebuild below depends on that.
	if p, ok := e.store.Project(dc.Project.ID); ok {
		dc.Project = p
	}
	if err := e.ReconcileRouting(); err != nil {
		return fmt.Errorf("updating Traefik routing: %w", err)
	}

	hosts := traefik.Hosts(&dc.Settings, dc.Project, dc.Spec.Spec)
	scheme := "http"
	if dc.Settings.TLSEnabled() {
		scheme = "https"
	}
	for _, h := range hosts {
		if h.RedirectTo == "" {
			dc.Log.Printf("Live at %s://%s%s", scheme, h.Domain, h.Path)
		}
	}
	if len(hosts) == 0 {
		dc.Log.Printf("No domains configured. Add one under the project's Domains tab.")
	}
	return nil
}

// reap removes the generation that was live before this deploy, after
// letting it finish whatever requests it had already accepted.
func (e *Engine) reap(ctx context.Context, dc *Context) error {
	drain := dc.Spec.Release.Drain.D()
	if dc.Spec.Kind == deployspec.KindCompose {
		// A compose stack replaced itself in place; there is no second
		// generation to reap.
		return e.pruneImages(ctx, dc.Project)
	}

	if drain > 0 {
		dc.Log.Printf("Draining the previous deployment for %s", drain)
		select {
		case <-time.After(drain):
		case <-ctx.Done():
		}
	}
	if err := e.stopGeneration(context.WithoutCancel(ctx), dc.Project.ID, dc.Deployment.ID, dc.Spec.Release.Drain.D()); err != nil {
		return err
	}
	return e.pruneImages(context.WithoutCancel(ctx), dc.Project)
}

// stopGeneration removes every container of a project except the one
// deployment named by keep.
func (e *Engine) stopGeneration(ctx context.Context, projectID, keep string, grace time.Duration) error {
	containers, err := e.docker.ListContainers(ctx, true, traefik.ProjectSelector(projectID)...)
	if err != nil {
		return err
	}
	if grace <= 0 || grace > 30*time.Second {
		grace = 10 * time.Second
	}

	var errs []string
	for _, c := range containers {
		if c.Labels[traefik.LabelDeployment] == keep {
			continue
		}
		if c.Labels[traefik.LabelRole] == traefik.RoleCron {
			continue // a running scheduled job is not a stale generation
		}
		if err := e.docker.StopContainer(ctx, c.ID, grace); err != nil {
			errs = append(errs, fmt.Sprintf("stopping %s: %v", c.Name(), err))
			continue
		}
		if err := e.docker.RemoveContainer(ctx, c.ID, true, false); err != nil {
			errs = append(errs, fmt.Sprintf("removing %s: %v", c.Name(), err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// rollbackNewGeneration tears down a deployment that failed before cutover,
// leaving whatever was live still live.
func (e *Engine) rollbackNewGeneration(dc *Context) {
	if !deployspec.Bool(dc.Spec.Release.AutoRollback, true) {
		dc.Log.Printf("Leaving the failed containers in place (release.autoRollback is false).")
		return
	}
	// The parent context is likely already cancelled or failing; cleanup
	// needs its own deadline or it would be skipped exactly when it matters.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if dc.Spec.Kind == deployspec.KindCompose {
		e.rollbackCompose(ctx, dc)
		return
	}

	containers, err := e.docker.ListContainers(ctx, true, traefik.DeploymentSelector(dc.Project.ID, dc.Deployment.ID)...)
	if err != nil {
		return
	}
	for _, c := range containers {
		_ = e.docker.StopContainer(ctx, c.ID, 5*time.Second)
		_ = e.docker.RemoveContainer(ctx, c.ID, true, false)
	}
	if len(containers) > 0 {
		dc.Log.Printf("Removed the failed deployment's containers; the previous deployment is still live.")
	}
}

// dumpContainerLogs relays the tail of each new container's output into the
// build log, which is where the reason for a health failure almost always is.
func (e *Engine) dumpContainerLogs(ctx context.Context, dc *Context, containers []string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	for _, id := range containers {
		info, err := e.docker.InspectContainer(ctx, id)
		name := id
		if err == nil {
			name = info.Name
			if !info.State.Running {
				dc.Log.Printf("%s exited with code %d", name, info.State.ExitCode)
			}
		}
		dc.Log.Printf("--- last output from %s ---", name)
		w := dc.Log.Writer("stderr")
		_ = e.docker.ContainerLogs(ctx, id, dockerapi.LogOptions{
			Stdout: true, Stderr: true, Tail: "50",
		}, w)
		w.Flush()
	}
}
