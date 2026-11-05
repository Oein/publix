package engine

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

const (
	// nfsHelperImage creates directories on an export. publix has no mount
	// of its own to do that through: only the daemon mounts NFS.
	nfsHelperImage = "busybox:1.37.0"
	// nfsLabel marks publix's docker volumes with the volume they serve.
	nfsLabel = "publix.nfs"
	// nfsPort is the NFS service. Probing it first matters: a mount aimed
	// at a dead server hangs in the daemon for about three minutes before
	// failing, which is a long time to leave a deploy with no explanation.
	nfsPort = "2049"

	nfsProbeTimeout  = 3 * time.Second
	nfsHelperTimeout = 90 * time.Second
	nfsStartTimeout  = 2 * time.Minute
	nfsSweepEvery    = 30 * time.Second

	// nfsMountFailure is what the daemon records on a container it could
	// not start because a volume would not mount. It is what separates a
	// container waiting for the NAS from one an operator stopped.
	nfsMountFailure = "error while mounting volume"
)

// NFSReachable reports whether the volume's server answers on the NFS port.
func NFSReachable(ctx context.Context, v store.Volume) error {
	ctx, cancel := context.WithTimeout(ctx, nfsProbeTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(v.NFS.Server, nfsPort))
	if err != nil {
		return fmt.Errorf("NFS server %s is not answering on port %s: %w", v.NFS.Server, nfsPort, err)
	}
	conn.Close()
	return nil
}

// ensureNFSVolume makes the docker volume for one directory of an export
// and returns its name.
func (e *Engine) ensureNFSVolume(ctx context.Context, v store.Volume, rel string) (string, error) {
	name := v.NFSDockerVolume(rel)
	labels := map[string]string{nfsLabel: v.Name}
	if err := e.docker.CreateVolume(ctx, name, "local", v.NFSDriverOptions(rel), labels); err != nil {
		return "", fmt.Errorf("creating the docker volume for %q: %w", v.Name, err)
	}
	return name, nil
}

// prepareNFS gets a mount ready and returns the docker volume to bind.
//
// A directory has to exist on the server before it can be mounted, so a
// helper goes in through the export's root first. Read-only mounts only
// check, for the same reason resolveVolumes does: there is nothing to write,
// and a chmod fails outright on a root-squashed export.
func (e *Engine) prepareNFS(ctx context.Context, v store.Volume, projectID, subPath string, readOnly bool) (string, error) {
	if err := NFSReachable(ctx, v); err != nil {
		return "", fmt.Errorf("volume %q: %w", v.Name, err)
	}
	var dirs []string
	if base := v.NFSRel(projectID, ""); base != "" {
		dirs = append(dirs, base)
	}
	rel := v.NFSRel(projectID, subPath)
	if rel != "" && rel != v.NFSRel(projectID, "") {
		dirs = append(dirs, rel)
	}
	if len(dirs) > 0 {
		if err := e.nfsHelper(ctx, v, dirs, readOnly); err != nil {
			return "", fmt.Errorf("volume %q: %w", v.Name, err)
		}
	}
	return e.ensureNFSVolume(ctx, v, rel)
}

// CheckNFS proves a volume works before it is saved: the server answers, the
// export mounts, and it can be written to unless the volume is read-only.
func (e *Engine) CheckNFS(ctx context.Context, v store.Volume) error {
	if err := NFSReachable(ctx, v); err != nil {
		return err
	}
	return e.nfsHelper(ctx, v, nil, v.ReadOnly)
}

// nfsHelper runs a throwaway container with the export's root mounted. The
// directories are passed as arguments, never spliced into the script, so a
// path cannot become a command.
func (e *Engine) nfsHelper(ctx context.Context, v store.Volume, dirs []string, readOnly bool) error {
	ctx, cancel := context.WithTimeout(ctx, nfsHelperTimeout)
	defer cancel()

	if err := e.ensureImage(ctx, nfsHelperImage, nil); err != nil {
		return err
	}
	root, err := e.ensureNFSVolume(ctx, v, "")
	if err != nil {
		return err
	}

	mode := ":nocopy"
	script := `cd /v || exit 1; [ -w . ] || { echo "export is not writable by root; check its root squash setting" >&2; exit 1; }; for d in "$@"; do mkdir -p "$d" && chmod 0777 "$d" || exit 1; done`
	if readOnly {
		mode = ":ro,nocopy"
		script = `cd /v || exit 1; for d in "$@"; do [ -d "$d" ] || { echo "$d does not exist on the export" >&2; exit 1; }; done`
	}

	created, err := e.docker.CreateContainer(ctx, "publix-nfs-helper-"+store.NewID(), &dockerapi.CreateConfig{
		Image:      nfsHelperImage,
		Entrypoint: []string{"sh", "-c", script, "publix-nfs-helper"},
		Cmd:        dirs,
		Labels:     map[string]string{nfsLabel + ".helper": v.Name},
		HostConfig: &dockerapi.HostConfig{
			NetworkMode:   "none",
			Binds:         []string{root + ":/v" + mode},
			RestartPolicy: &dockerapi.RestartPolicy{Name: "no"},
		},
	})
	if err != nil {
		return fmt.Errorf("creating the NFS helper: %w", err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		_ = e.docker.RemoveContainer(cleanup, created.ID, true, false)
	}()

	if err := e.docker.StartContainer(ctx, created.ID); err != nil {
		return fmt.Errorf("mounting %s: %w", v.Dir(""), err)
	}
	code, err := e.docker.WaitContainer(ctx, created.ID)
	if err != nil {
		return err
	}
	if code != 0 {
		var out bytes.Buffer
		logCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		_ = e.docker.ContainerLogs(logCtx, created.ID, dockerapi.LogOptions{Stdout: true, Stderr: true}, &out)
		return fmt.Errorf("preparing %s failed: %s", v.Dir(""), strings.TrimSpace(lastLines(out.String(), 3)))
	}
	return nil
}

// NFSWatcher starts containers the daemon gave up on while a NAS was away.
//
// Docker tries a restart-policy container once at boot. If the NAS is slower
// to come up than the host, which is the normal order after a power cut, the
// mount fails, the container stays stopped, and nothing ever tries again.
type NFSWatcher struct {
	engine *Engine
	store  *store.Store
	// OnStart and OnError are told what the sweep did. Either may be nil.
	OnStart func(container string)
	OnError func(container string, err error)
}

// NewNFSWatcher creates a watcher.
func NewNFSWatcher(e *Engine, st *store.Store) *NFSWatcher {
	return &NFSWatcher{engine: e, store: st}
}

// Run sweeps until ctx ends.
func (w *NFSWatcher) Run(ctx context.Context) {
	t := time.NewTicker(nfsSweepEvery)
	defer t.Stop()
	for {
		w.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *NFSWatcher) sweep(ctx context.Context) {
	set := w.store.Settings()
	byName := map[string]store.Volume{}
	for _, v := range set.Volumes {
		if v.IsNFS() {
			byName[v.Name] = v
		}
	}
	if len(byName) == 0 {
		return
	}
	docker := w.engine.docker
	vols, err := docker.ListVolumes(ctx, nfsLabel)
	if err != nil || len(vols) == 0 {
		return
	}
	owners := map[string]string{}
	for _, dv := range vols {
		owners[dv.Name] = dv.Labels[nfsLabel]
	}
	containers, err := docker.ListContainers(ctx, true)
	if err != nil {
		return
	}

	reachable := map[string]bool{}
	serverUp := func(v store.Volume) bool {
		if up, ok := reachable[v.NFS.Server]; ok {
			return up
		}
		up := NFSReachable(ctx, v) == nil
		reachable[v.NFS.Server] = up
		return up
	}

	for _, c := range nfsWaiting(containers, owners) {
		ready := true
		for _, owner := range nfsOwners(c, owners) {
			v, ok := byName[owner]
			if !ok || !serverUp(v) {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		info, err := docker.InspectContainer(ctx, c.ID)
		if err != nil || !strings.Contains(info.State.Error, nfsMountFailure) {
			continue
		}
		startCtx, cancel := context.WithTimeout(ctx, nfsStartTimeout)
		err = docker.StartContainer(startCtx, c.ID)
		cancel()
		if err != nil {
			if w.OnError != nil {
				w.OnError(c.Name(), err)
			}
			continue
		}
		if w.OnStart != nil {
			w.OnStart(c.Name())
		}
	}
}

// nfsWaiting narrows containers to those not running with an NFS volume
// mounted. Whether one was stopped on purpose takes an inspect to learn, so
// this is only the cheap half of the question.
func nfsWaiting(containers []dockerapi.Container, owners map[string]string) []dockerapi.Container {
	var out []dockerapi.Container
	for _, c := range containers {
		if c.State != "created" && c.State != "exited" {
			continue
		}
		if len(nfsOwners(c, owners)) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// nfsOwners lists the publix volumes a container's mounts belong to.
func nfsOwners(c dockerapi.Container, owners map[string]string) []string {
	var out []string
	for _, m := range c.Mounts {
		if owner, ok := owners[m.Name]; ok {
			out = append(out, owner)
		}
	}
	return out
}
