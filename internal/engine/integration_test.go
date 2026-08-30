package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// These tests drive a real Docker daemon. They are the only way to know
// that the pieces actually compose: a mocked docker client would happily
// confirm a deploy flow that falls apart against the real API.
func dockerOrSkip(t *testing.T) *dockerapi.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping docker integration test in short mode")
	}
	c, err := dockerapi.New()
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Ping(ctx); err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	return c
}

// harness is a fully wired platform pointed at temporary directories.
type harness struct {
	t       *testing.T
	engine  *Engine
	store   *store.Store
	docker  *dockerapi.Client
	logs    *buildlog.Store
	home    string
	traefik string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	docker := dockerOrSkip(t)

	home := t.TempDir()
	t.Setenv("PUBLIX_HOME", home)

	st, err := store.OpenAt(filepath.Join(home, "publix.json"))
	if err != nil {
		t.Fatal(err)
	}
	traefikDir := filepath.Join(home, "traefik")
	network := "publix-test-" + store.NewID()

	if err := st.SetSettings(func(s *store.Settings) error {
		s.Network = network
		s.TraefikDynamicDir = traefikDir
		s.WorkDir = filepath.Join(home, "work")
		s.AppsDomain = "test.local"
		s.CertResolver = "" // no ACME in a test
		s.KeepImages = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	logs, err := buildlog.NewStore(filepath.Join(home, "logs"))
	if err != nil {
		t.Fatal(err)
	}

	h := &harness{
		t: t, store: st, docker: docker, logs: logs,
		home: home, traefik: traefikDir,
		engine: New(st, docker, logs),
	}
	t.Cleanup(h.cleanup)
	return h
}

// cleanup removes every container, image and network the test created. A
// test that leaks docker state poisons the next run.
func (h *harness) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	for _, p := range h.store.Projects() {
		_ = h.engine.Teardown(ctx, p)
	}
	set := h.store.Settings()
	if nets, err := h.docker.ListNetworks(ctx); err == nil {
		for _, n := range nets {
			if n.Name == set.Network {
				exec.CommandContext(ctx, "docker", "network", "rm", n.Name).Run()
			}
		}
	}
}

// project registers a project whose checkout is a directory of files.
func (h *harness) project(name string, files map[string]string) *store.Project {
	h.t.Helper()
	p, err := h.store.CreateProject(&store.Project{Name: name})
	if err != nil {
		h.t.Fatal(err)
	}
	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	return p
}

// deploy runs a deployment synchronously and returns the finished record.
func (h *harness) deploy(p *store.Project, opt Options) *store.Deployment {
	h.t.Helper()
	dep, err := h.engine.Deploy(p.ID, opt)
	if err != nil {
		h.t.Fatalf("queueing the deploy: %v", err)
	}
	return h.await(p.ID, dep.ID, 5*time.Minute)
}

// await waits for a deployment to finish completely.
//
// A terminal status is not sufficient: the engine marks a deployment live at
// cutover, which is correct — it *is* serving — but reaping the previous
// generation and pruning images happen afterwards. Tests that assert on
// containers or images have to wait for the engine to be idle as well.
func (h *harness) await(projectID, deploymentID string, timeout time.Duration) *store.Deployment {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p, _ := h.store.Project(projectID)
		dep, ok := p.Deployment(deploymentID)
		if ok && dep.Status.Terminal() {
			if _, busy := h.engine.Running(projectID); !busy {
				return dep
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	h.dumpLog(deploymentID)
	h.t.Fatalf("deployment %s did not finish within %s", deploymentID, timeout)
	return nil
}

func (h *harness) dumpLog(deploymentID string) {
	h.t.Helper()
	lines, _ := h.logs.Read(deploymentID)
	h.t.Logf("--- build log for %s ---", deploymentID)
	for _, l := range lines {
		h.t.Logf("  %s", l.Text)
	}
}

func (h *harness) mustSucceed(dep *store.Deployment) {
	h.t.Helper()
	if dep.Status != store.StatusLive {
		h.dumpLog(dep.ID)
		h.t.Fatalf("deployment ended %s: %s", dep.Status, dep.Error)
	}
}

// A Dockerfile project should build, health-check and go live.
func TestDeployDockerfileEndToEnd(t *testing.T) {
	h := newHarness(t)
	p := h.project("hello", map[string]string{
		"deployment.yaml": "port: 80\nhealth:\n  type: http\n  path: /\n  grace: 60s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo 'v1' > /usr/share/nginx/html/index.html\nEXPOSE 80\n",
	})

	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	if dep.Image == "" {
		t.Error("the deployment recorded no image")
	}
	if dep.Kind != "dockerfile" {
		t.Errorf("kind = %q, want dockerfile", dep.Kind)
	}

	p, _ = h.store.Project(p.ID)
	if p.Current != dep.ID {
		t.Errorf("current = %q, want the new deployment %q", p.Current, dep.ID)
	}

	// The container should really be serving.
	ctx := context.Background()
	containers, err := h.docker.ListContainers(ctx, false, traefik.DeploymentSelector(p.ID, dep.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d containers, want 1", len(containers))
	}
	body := httpGet(t, h, containers[0].ID, 80, "/")
	if !strings.Contains(body, "v1") {
		t.Errorf("served %q, want it to contain v1", body)
	}

	// And Traefik's routing file should point at it.
	raw, err := os.ReadFile(filepath.Join(h.traefik, "publix.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want := traefik.ServiceName(p.Slug, dep.ID) + "@docker"
	if !strings.Contains(string(raw), want) {
		t.Errorf("routing file does not reference %s:\n%s", want, raw)
	}
	if !strings.Contains(string(raw), "hello.test.local") {
		t.Errorf("routing file has no generated host:\n%s", raw)
	}
}

// Only one generation may remain resident after a deploy: that is the
// resource guarantee the whole design is built around.
func TestRedeployLeavesOneGeneration(t *testing.T) {
	h := newHarness(t)
	p := h.project("single", map[string]string{
		"deployment.yaml": "port: 80\nrelease:\n  drain: 1s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo v1 > /usr/share/nginx/html/index.html\n",
	})

	first := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(first)

	// A different commit means a different image tag, so this really
	// rebuilds rather than reusing the first image.
	second := h.deploy(p, Options{Trigger: "test", Commit: "0000000000000000000000000000000000000002"})
	h.mustSucceed(second)

	ctx := context.Background()
	containers, err := h.docker.ListContainers(ctx, true, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		var names []string
		for _, c := range containers {
			names = append(names, c.Name()+"="+c.State)
		}
		t.Errorf("got %d containers after a redeploy, want exactly 1: %v", len(containers), names)
	}
	if len(containers) == 1 && containers[0].Labels[traefik.LabelDeployment] != second.ID {
		t.Errorf("the surviving container belongs to %q, want the new deployment %q",
			containers[0].Labels[traefik.LabelDeployment], second.ID)
	}
}

// A deployment that never becomes healthy must not take the live one down.
func TestFailedHealthGateLeavesPreviousLive(t *testing.T) {
	h := newHarness(t)
	p := h.project("gated", map[string]string{
		"deployment.yaml": "port: 80\nhealth:\n  type: http\n  path: /\n  grace: 20s\n  interval: 1s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo good > /usr/share/nginx/html/index.html\n",
	})

	good := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(good)

	// Replace the app with one that exits immediately.
	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM alpine\nCMD [\"sh\", \"-c\", \"exit 1\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := h.deploy(p, Options{Trigger: "test", Commit: "0000000000000000000000000000000000000009"})
	if bad.Status != store.StatusFailed {
		h.dumpLog(bad.ID)
		t.Fatalf("a broken deployment ended %s, want failed", bad.Status)
	}

	p, _ = h.store.Project(p.ID)
	if p.Current != good.ID {
		t.Errorf("current = %q, want the previously healthy deployment %q to still be live", p.Current, good.ID)
	}

	// The failed generation must have been cleaned up, and the healthy one
	// must still be running.
	ctx := context.Background()
	containers, err := h.docker.ListContainers(ctx, true, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range containers {
		if c.Labels[traefik.LabelDeployment] == bad.ID {
			t.Errorf("the failed deployment's container %s was left behind", c.Name())
		}
	}
	live := 0
	for _, c := range containers {
		if c.Labels[traefik.LabelDeployment] == good.ID && c.State == "running" {
			live++
		}
	}
	if live != 1 {
		t.Errorf("got %d running containers for the healthy deployment, want 1", live)
	}
}

// Rolling back re-deploys the earlier commit. Because its image is still on
// disk, it should not rebuild.
func TestRollbackReusesRetainedImage(t *testing.T) {
	h := newHarness(t)
	p := h.project("rollback", map[string]string{
		"deployment.yaml": "port: 80\nrelease:\n  drain: 1s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo v1 > /usr/share/nginx/html/index.html\n",
	})

	v1 := h.deploy(p, Options{Trigger: "test", Commit: "1111111111111111111111111111111111111111"})
	h.mustSucceed(v1)

	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM nginx:alpine\nRUN echo v2 > /usr/share/nginx/html/index.html\n"), 0o644)
	v2 := h.deploy(p, Options{Trigger: "test", Commit: "2222222222222222222222222222222222222222"})
	h.mustSucceed(v2)

	ctx := context.Background()
	plan, err := h.engine.PlanRollback(ctx, p.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Instant {
		t.Errorf("rolling back one step should be instant, got: %s", plan.Reason)
	}

	dep, err := h.engine.Rollback(p.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolled := h.await(p.ID, dep.ID, 3*time.Minute)
	h.mustSucceed(rolled)

	// The rollback must serve v1's content again.
	containers, err := h.docker.ListContainers(ctx, false, traefik.DeploymentSelector(p.ID, rolled.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d containers after rollback", len(containers))
	}
	if body := httpGet(t, h, containers[0].ID, 80, "/"); !strings.Contains(body, "v1") {
		t.Errorf("after rolling back, served %q, want v1", body)
	}
	if rolled.RolledBackFrom != v2.ID {
		t.Errorf("rolledBackFrom = %q, want %q", rolled.RolledBackFrom, v2.ID)
	}
}

// Only two images per project may survive.
func TestImageRetentionKeepsTwo(t *testing.T) {
	h := newHarness(t)
	p := h.project("retention", map[string]string{
		"deployment.yaml": "port: 80\nrelease:\n  drain: 0s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo v1 > /usr/share/nginx/html/index.html\n",
	})

	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	for i := 1; i <= 3; i++ {
		os.WriteFile(filepath.Join(dir, "Dockerfile"),
			[]byte(fmt.Sprintf("FROM nginx:alpine\nRUN echo v%d > /usr/share/nginx/html/index.html\n", i)), 0o644)
		dep := h.deploy(p, Options{Trigger: "test", Commit: strings.Repeat(fmt.Sprint(i), 40)})
		h.mustSucceed(dep)
	}

	images, err := h.docker.ListImages(context.Background(), "publix.project="+p.ID)
	if err != nil {
		t.Fatal(err)
	}
	tagged := 0
	var tags []string
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag != "<none>:<none>" {
				tagged++
				tags = append(tags, tag)
			}
		}
	}
	if tagged > 2 {
		t.Errorf("got %d retained images, want at most 2: %v", tagged, tags)
	}
	if tagged < 2 {
		t.Errorf("got %d retained images, want 2 so a rollback stays instant: %v", tagged, tags)
	}
}

// A shared volume must land in the project's own subdirectory, and two
// projects must not be able to see each other's files through it.
func TestSharedVolumeIsolation(t *testing.T) {
	h := newHarness(t)

	volRoot := filepath.Join(h.home, "shared")
	if err := os.MkdirAll(volRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSettings(func(s *store.Settings) error {
		s.SharedVolumes = []store.SharedVolume{{Name: "disk0", Path: volRoot}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	spec := "port: 80\nvolumes: [disk0]\nrelease:\n  drain: 0s\n"
	dockerfile := "FROM nginx:alpine\n"

	a := h.project("alpha", map[string]string{"deployment.yaml": spec, "Dockerfile": dockerfile})
	b := h.project("beta", map[string]string{"deployment.yaml": spec, "Dockerfile": dockerfile})

	h.mustSucceed(h.deploy(a, Options{Trigger: "test"}))
	h.mustSucceed(h.deploy(b, Options{Trigger: "test"}))

	// Each project's directory must exist under the volume root, named by
	// project ID.
	for _, p := range []*store.Project{a, b} {
		dir := filepath.Join(volRoot, p.ID)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("shared volume directory %s was not created for %s", dir, p.Name)
		}
	}

	ctx := context.Background()
	// Write a file from inside alpha's container, then prove beta cannot
	// see it — the isolation guarantee stated in the settings model.
	alphaC := h.oneContainer(ctx, a.ID)
	if _, err := h.docker.Exec(ctx, alphaC, []string{"sh", "-c", "echo secret > /shared/disk0/alpha.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(volRoot, a.ID, "alpha.txt")); err != nil {
		t.Errorf("the file written inside the container did not appear on the host: %v", err)
	}

	betaC := h.oneContainer(ctx, b.ID)
	res, err := h.docker.Exec(ctx, betaC, []string{"sh", "-c", "ls /shared/disk0"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, "alpha.txt") {
		t.Errorf("beta can see alpha's files through the shared volume: %q", res.Stdout)
	}
}

// A volume the deployment.yaml asks for but the server has not registered
// must fail the deploy with a message naming what is missing.
func TestUnregisteredVolumeFailsClearly(t *testing.T) {
	h := newHarness(t)
	p := h.project("needsvol", map[string]string{
		"deployment.yaml": "port: 80\nvolumes: [nosuchdisk]\n",
		"Dockerfile":      "FROM nginx:alpine\n",
	})
	dep := h.deploy(p, Options{Trigger: "test"})
	if dep.Status != store.StatusFailed {
		t.Fatalf("deploy ended %s, want failed", dep.Status)
	}
	if !strings.Contains(dep.Error, "nosuchdisk") {
		t.Errorf("the error should name the missing volume, got: %s", dep.Error)
	}
}

// A static site should build on the host and be packaged into an image.
func TestDeployStaticSite(t *testing.T) {
	h := newHarness(t)
	p := h.project("site", map[string]string{
		"deployment.yaml": "type: static\nbuild:\n  command: \"mkdir -p dist && echo hello-static > dist/index.html\"\n  output: dist\n",
	})
	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	ctx := context.Background()
	if body := httpGet(t, h, h.oneContainer(ctx, p.ID), 80, "/"); !strings.Contains(body, "hello-static") {
		t.Errorf("static site served %q", body)
	}
}

// oneContainer returns the single running container of a project.
func (h *harness) oneContainer(ctx context.Context, projectID string) string {
	h.t.Helper()
	containers, err := h.docker.ListContainers(ctx, false, traefik.ProjectSelector(projectID)...)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(containers) != 1 {
		h.t.Fatalf("got %d running containers for %s, want 1", len(containers), projectID)
	}
	return containers[0].ID
}

// httpGet fetches a path from a container over the docker network, which is
// the same route publix's own health probe takes.
func httpGet(t *testing.T, h *harness, containerID string, port int, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	info, err := h.docker.InspectContainer(ctx, containerID)
	if err != nil {
		t.Fatal(err)
	}
	ip := info.IPOn(h.store.Settings().Network)
	if ip == "" {
		t.Fatal("container has no network address")
	}

	addr := net.JoinHostPort(ip, fmt.Sprint(port))
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		return string(buf[:n])
	}
	t.Fatalf("could not reach %s: %v", addr, lastErr)
	return ""
}
