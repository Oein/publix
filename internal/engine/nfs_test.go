package engine

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

func TestBindModes(t *testing.T) {
	cases := []struct {
		b    Bind
		want string
	}{
		{Bind{HostPath: "/a", MountPath: "/b"}, "/a:/b"},
		{Bind{HostPath: "/a", MountPath: "/b", ReadOnly: true}, "/a:/b:ro"},
		{Bind{HostPath: "v", MountPath: "/b", NoCopy: true}, "v:/b:nocopy"},
		{Bind{HostPath: "v", MountPath: "/b", ReadOnly: true, NoCopy: true}, "v:/b:ro,nocopy"},
	}
	for _, c := range cases {
		if got := c.b.String(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestNFSWaitingOnlyPicksStoppedContainersOnNFS(t *testing.T) {
	owners := map[string]string{"publix-nfs-data-abc": "data"}
	onNFS := []dockerapi.ContainerMount{{Type: "volume", Name: "publix-nfs-data-abc"}}

	containers := []dockerapi.Container{
		{ID: "running", State: "running", Mounts: onNFS},
		{ID: "restarting", State: "restarting", Mounts: onNFS},
		{ID: "paused", State: "paused", Mounts: onNFS},
		{ID: "created", State: "created", Mounts: onNFS},
		{ID: "exited", State: "exited", Mounts: onNFS},
		{ID: "other-volume", State: "exited", Mounts: []dockerapi.ContainerMount{{Type: "volume", Name: "pgdata"}}},
		{ID: "bind-only", State: "exited", Mounts: []dockerapi.ContainerMount{{Type: "bind"}}},
	}

	var got []string
	for _, c := range nfsWaiting(containers, owners) {
		got = append(got, c.ID)
	}
	if strings.Join(got, ",") != "created,exited" {
		t.Fatalf("got %v", got)
	}
}

// TestNFSLive needs a real export, which is why it asks for one:
//
//	PUBLIX_TEST_NFS=server:/export/scratch go test ./internal/engine -run TestNFSLive
//
// It writes only inside a new directory under that export, then removes it.
func TestNFSLive(t *testing.T) {
	target := os.Getenv("PUBLIX_TEST_NFS")
	if target == "" {
		t.Skip("PUBLIX_TEST_NFS is not set")
	}
	docker := dockerOrSkip(t)
	server, export, ok := strings.Cut(target, ":")
	if !ok {
		t.Fatalf("PUBLIX_TEST_NFS = %q, want server:/export", target)
	}
	e := &Engine{docker: docker}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	project := "nfstest-" + store.NewID()
	v := store.Volume{Name: "nfstest", Scope: store.ScopeProject, NFS: &store.NFSMount{Server: server, Export: export}}

	removeVolumes := func() {
		vols, _ := docker.ListVolumes(context.Background(), nfsLabel+"=nfstest")
		for _, dv := range vols {
			_ = docker.RemoveVolume(context.Background(), dv.Name, true)
		}
	}
	defer removeVolumes()
	defer func() {
		root := v
		root.Scope = store.ScopeShared
		if err := e.nfsRemove(context.Background(), root, project); err != nil {
			t.Logf("cleanup: %v", err)
		}
	}()

	if err := e.CheckNFS(ctx, v); err != nil {
		t.Fatalf("CheckNFS: %v", err)
	}

	roMissing := v
	roMissing.ReadOnly = true
	if _, err := e.prepareNFS(ctx, roMissing, project, "", true); err == nil {
		t.Fatal("a read-only mount of a missing directory must fail, not create it")
	}

	name, err := e.prepareNFS(ctx, v, project, "repos", false)
	if err != nil {
		t.Fatalf("prepareNFS: %v", err)
	}
	if !strings.HasPrefix(name, store.NFSVolumePrefix) {
		t.Fatalf("name = %q", name)
	}

	// The whole point: a file written through the subdirectory's volume is
	// on the export, seen again through a second, separate mount.
	run := func(bind, script string) (int, string) {
		created, err := docker.CreateContainer(ctx, "publix-nfstest-"+store.NewID(), &dockerapi.CreateConfig{
			Image:      nfsHelperImage,
			Entrypoint: []string{"sh", "-c", script},
			HostConfig: &dockerapi.HostConfig{NetworkMode: "none", Binds: []string{bind}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer docker.RemoveContainer(context.Background(), created.ID, true, false)
		if err := docker.StartContainer(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
		code, err := docker.WaitContainer(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		return code, ""
	}
	if code, _ := run(name+":/data:nocopy", "grep -q ' /data nfs' /proc/mounts && echo hello > /data/f"); code != 0 {
		t.Fatalf("writing through the volume exited %d", code)
	}
	root, err := e.ensureNFSVolume(ctx, v, "")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := run(root+":/v:ro,nocopy", "grep -q hello /v/"+project+"/repos/f"); code != 0 {
		t.Fatalf("the file is not on the export where the layout says (exit %d)", code)
	}

	// The boot race, replayed. The directory a volume points at does not
	// exist yet, so the daemon fails the mount exactly as it does when the
	// NAS is late, and leaves the container stopped.
	t.Setenv("PUBLIX_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(func(set *store.Settings) error {
		set.Volumes = append(set.Volumes, v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.store = st

	rel := project + "/late"
	late, err := e.ensureNFSVolume(ctx, v, rel)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := docker.CreateContainer(ctx, "publix-nfstest-"+store.NewID(), &dockerapi.CreateConfig{
		Image:      nfsHelperImage,
		Entrypoint: []string{"sleep", "60"},
		HostConfig: &dockerapi.HostConfig{
			NetworkMode:   "none",
			Binds:         []string{late + ":/data:nocopy"},
			RestartPolicy: &dockerapi.RestartPolicy{Name: "unless-stopped"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer docker.RemoveContainer(context.Background(), waiting.ID, true, false)
	if err := docker.StartContainer(ctx, waiting.ID); err == nil {
		t.Fatal("the mount should have failed: its directory does not exist")
	}

	// A control: stopped by an operator, on the same export. The sweep has
	// to leave this one exactly as it found it.
	if err := e.nfsHelper(ctx, v, []string{project + "/stopped"}, false); err != nil {
		t.Fatal(err)
	}
	stoppedVol, err := e.ensureNFSVolume(ctx, v, project+"/stopped")
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := docker.CreateContainer(ctx, "publix-nfstest-"+store.NewID(), &dockerapi.CreateConfig{
		Image:      nfsHelperImage,
		Entrypoint: []string{"sleep", "60"},
		HostConfig: &dockerapi.HostConfig{NetworkMode: "none", Binds: []string{stoppedVol + ":/data:nocopy"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer docker.RemoveContainer(context.Background(), stopped.ID, true, false)
	if err := docker.StartContainer(ctx, stopped.ID); err != nil {
		t.Fatal(err)
	}
	if err := docker.StopContainer(ctx, stopped.ID, time.Second); err != nil {
		t.Fatal(err)
	}

	var started []string
	w := NewNFSWatcher(e, st)
	w.OnStart = func(c string) { started = append(started, c) }
	w.OnError = func(c string, err error) { t.Logf("still failing %s: %v", c, err) }

	// The directory still does not exist, so this attempt fails again and
	// the container must stay in the queue for the next sweep.
	w.sweep(ctx)
	if len(started) != 0 {
		t.Fatalf("started %v while the mount could not work", started)
	}

	// The NAS is back.
	if err := e.nfsHelper(ctx, v, []string{rel}, false); err != nil {
		t.Fatal(err)
	}
	w.sweep(ctx)
	if len(started) != 1 {
		t.Fatalf("started %v, want only the waiting container", started)
	}
	info, err := docker.InspectContainer(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !info.State.Running {
		t.Fatalf("the waiting container is %s: %s", info.State.Status, info.State.Error)
	}
	if info, err := docker.InspectContainer(ctx, stopped.ID); err != nil || info.State.Running {
		t.Fatalf("the sweep started a container an operator stopped (err %v)", err)
	}
}

// nfsRemove deletes a directory under an export. publix itself never deletes
// data from a volume, which is why this lives with the tests.
func (e *Engine) nfsRemove(ctx context.Context, v store.Volume, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, nfsHelperTimeout)
	defer cancel()
	root, err := e.ensureNFSVolume(ctx, v, "")
	if err != nil {
		return err
	}
	created, err := e.docker.CreateContainer(ctx, "publix-nfs-helper-"+store.NewID(), &dockerapi.CreateConfig{
		Image:      nfsHelperImage,
		Entrypoint: []string{"sh", "-c", `rm -rf "/v/$1"`, "publix-nfs-helper", dir},
		HostConfig: &dockerapi.HostConfig{NetworkMode: "none", Binds: []string{root + ":/v:nocopy"}},
	})
	if err != nil {
		return err
	}
	defer e.docker.RemoveContainer(context.WithoutCancel(ctx), created.ID, true, false)
	if err := e.docker.StartContainer(ctx, created.ID); err != nil {
		return err
	}
	_, err = e.docker.WaitContainer(ctx, created.ID)
	return err
}
