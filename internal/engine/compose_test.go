package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// A compose stack must come up, be reachable on the publix network, and be
// attributed to its project.
func TestDeployComposeStack(t *testing.T) {
	h := newHarness(t)
	p := h.project("stack", map[string]string{
		"deployment.yaml": "type: compose\nservice: web\nport: 80\n",
		"docker-compose.yml": `
services:
  web:
    image: nginx:alpine
    volumes:
      - ./site:/usr/share/nginx/html:ro
  cache:
    image: alpine
    command: ["sh", "-c", "sleep 3600"]
`,
		"site/index.html": "compose-v1",
	})

	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	if dep.Kind != "compose" {
		t.Errorf("kind = %q, want compose", dep.Kind)
	}

	ctx := context.Background()
	containers, err := h.docker.ListContainers(ctx, false,
		"com.docker.compose.project="+traefik.ComposeProject(p.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 {
		var names []string
		for _, c := range containers {
			names = append(names, c.Name())
		}
		t.Fatalf("got %d containers, want both services: %v", len(containers), names)
	}

	var web string
	for _, c := range containers {
		if c.Labels["com.docker.compose.service"] == "web" {
			web = c.ID
		}
		// Every service must be attributed to the project, so the
		// dashboard and teardown can find the whole stack.
		if c.Labels[traefik.LabelProject] != p.ID {
			t.Errorf("%s is missing the publix.project label", c.Name())
		}
	}
	if web == "" {
		t.Fatal("the web service has no container")
	}

	// The routed service must be attached to the shared network, or
	// Traefik could never reach it.
	info, err := h.docker.InspectContainer(ctx, web)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := info.NetworkSettings.Networks[h.store.Settings().Network]; !ok {
		var nets []string
		for n := range info.NetworkSettings.Networks {
			nets = append(nets, n)
		}
		t.Errorf("the web container is not on the %q network, only %v", h.store.Settings().Network, nets)
	}
	// The non-routed service must NOT be, so a stack's internals stay off
	// the shared network.
	for _, c := range containers {
		if c.Labels["com.docker.compose.service"] != "cache" {
			continue
		}
		ci, err := h.docker.InspectContainer(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ci.NetworkSettings.Networks[h.store.Settings().Network]; ok {
			t.Errorf("the cache service should not be exposed on the shared network")
		}
	}

	if body := httpGet(t, h, web, 80, "/"); !strings.Contains(body, "compose-v1") {
		t.Errorf("compose stack served %q", body)
	}

	// Traefik's routing must use the stack's stable service name.
	raw, err := os.ReadFile(filepath.Join(h.traefik, "publix.yml"))
	if err != nil {
		t.Fatal(err)
	}
	want := traefik.ServiceName(p.Slug, traefik.ComposeDeploymentKey) + "@docker"
	if !strings.Contains(string(raw), want) {
		t.Errorf("routing file should reference %s:\n%s", want, raw)
	}
}

// Redeploying a compose stack must replace it in place and keep its named
// volumes: a per-deployment compose project name would hand the stack a
// brand-new empty volume on every push.
func TestComposeRedeployKeepsNamedVolumeData(t *testing.T) {
	h := newHarness(t)
	p := h.project("stateful", map[string]string{
		"deployment.yaml": "type: compose\nservice: web\nport: 80\n",
		"docker-compose.yml": `
services:
  web:
    image: nginx:alpine
    volumes:
      - data:/data
volumes:
  data:
`,
	})

	h.mustSucceed(h.deploy(p, Options{Trigger: "test", Commit: strings.Repeat("1", 40)}))

	ctx := context.Background()
	first := h.oneComposeContainer(ctx, p, "web")
	if _, err := h.docker.Exec(ctx, first, []string{"sh", "-c", "echo persisted > /data/marker"}); err != nil {
		t.Fatal(err)
	}

	h.mustSucceed(h.deploy(p, Options{Trigger: "test", Commit: strings.Repeat("2", 40)}))

	second := h.oneComposeContainer(ctx, p, "web")
	res, err := h.docker.Exec(ctx, second, []string{"sh", "-c", "cat /data/marker 2>&1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "persisted") {
		t.Errorf("the named volume did not survive a redeploy; /data/marker was %q", res.Stdout+res.Stderr)
	}

	// Only one generation of the stack may remain.
	containers, err := h.docker.ListContainers(ctx, true,
		"com.docker.compose.project="+traefik.ComposeProject(p.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		t.Errorf("got %d containers after a compose redeploy, want 1", len(containers))
	}
}

// A registered volume must reach a compose service too.
func TestComposeVolume(t *testing.T) {
	h := newHarness(t)
	volRoot := filepath.Join(h.home, "shared")
	if err := os.MkdirAll(volRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetSettings(func(s *store.Settings) error {
		s.Volumes = []store.Volume{{Name: "disk0", Path: volRoot, Scope: store.ScopeProject}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	p := h.project("cvol", map[string]string{
		"deployment.yaml":    "type: compose\nservice: web\nport: 80\nvolumes: [disk0]\n",
		"docker-compose.yml": "services:\n  web:\n    image: nginx:alpine\n",
	})
	h.mustSucceed(h.deploy(p, Options{Trigger: "test"}))

	ctx := context.Background()
	c := h.oneComposeContainer(ctx, p, "web")
	if _, err := h.docker.Exec(ctx, c, []string{"sh", "-c", "echo hi > /shared/disk0/from-compose"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(volRoot, p.ID, "from-compose")); err != nil {
		t.Errorf("the shared volume was not mounted into the compose service: %v", err)
	}
}

func (h *harness) oneComposeContainer(ctx context.Context, p *store.Project, service string) string {
	h.t.Helper()
	containers, err := h.docker.ListContainers(ctx, false,
		"com.docker.compose.project="+traefik.ComposeProject(p.Slug),
		"com.docker.compose.service="+service)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(containers) != 1 {
		h.t.Fatalf("got %d containers for compose service %q, want 1", len(containers), service)
	}
	return containers[0].ID
}

// A published port has to reach the generated override in compose's own
// short syntax, or the stack comes up with the port unbound.
func TestComposePortRendering(t *testing.T) {
	for _, tc := range []struct {
		port deployspec.Port
		want string
	}{
		{deployspec.Port{Host: 2222}, "2222:2222/tcp"},
		{deployspec.Port{Host: 2222, Container: 22}, "2222:22/tcp"},
		{deployspec.Port{Host: 5432, Protocol: "udp"}, "5432:5432/udp"},
		{deployspec.Port{Host: 2222, Bind: "127.0.0.1"}, "127.0.0.1:2222:2222/tcp"},
	} {
		if got := composePort(tc.port); got != tc.want {
			t.Errorf("composePort(%+v) = %q, want %q", tc.port, got, tc.want)
		}
	}
}

// A service reached only over TCP has no hostname pointing at it, but it is
// still routed — and it must be both labelled and attached to the network
// Traefik can see, which is why both callers share this.
func TestRoutedServicesIncludesTCPOnlyServices(t *testing.T) {
	sp, err := deployspec.Parse([]byte(`
type: compose
compose: docker-compose.yml
service: frontend
port: 3000
tcp:
  - sni: [git.example.com]
    port: 2222
    passthrough: true
    service: backend
`))
	if err != nil {
		t.Fatal(err)
	}
	routed := routedServices(&deployspec.Resolved{Spec: sp})
	if !routed["backend"] {
		t.Error("a service reached only over TCP was not treated as routed")
	}
	if !routed["frontend"] {
		t.Error("the primary service was dropped")
	}
}

// The exact output that made this worth doing: a deploy run before CI had
// published the image, reported as "exit status 18".
func TestComposeFailureNamesAnUnpublishedImage(t *testing.T) {
	out := ` backend Pulling 
 frontend Error failed to resolve reference "ghcr.io/oe2n/giten-frontend:3d35112750f65f38471f82892928e069183d8699": ghcr.io/oe2n/giten-frontend:3d35112750f65f38471f82892928e069183d8699: not found
Error response from daemon: failed to resolve reference "ghcr.io/oe2n/giten-frontend:3d35112750f65f38471f82892928e069183d8699": not found`

	got := composeFailure(out)
	if got == "" {
		t.Fatal("the pull failure was not recognised")
	}
	if !strings.Contains(got, "not in the registry") {
		t.Errorf("the message does not say what is wrong:\n%s", got)
	}
	if !strings.Contains(got, "ghcr.io/oe2n/giten-frontend:3d35112750f65f38471f82892928e069183d8699") {
		t.Errorf("the message does not name the image:\n%s", got)
	}
	// Someone reading this has to know the running deployment is untouched,
	// or the obvious next move is to panic and redeploy.
	if !strings.Contains(got, "still serving") {
		t.Errorf("the message does not say what is still running:\n%s", got)
	}
}

func TestComposeFailureRecognisesADeniedPull(t *testing.T) {
	got := composeFailure(`Error response from daemon: pull access denied for ghcr.io/acme/private, repository does not exist or may require 'docker login'`)
	if !strings.Contains(got, "refused") {
		t.Errorf("a denied pull was not recognised:\n%s", got)
	}
	if !strings.Contains(got, "ghcr.io/acme/private") {
		t.Errorf("the message does not name the image:\n%s", got)
	}
}

// Anything else keeps the exit status rather than being explained wrongly.
func TestComposeFailureStaysQuietWhenItHasNothingToAdd(t *testing.T) {
	for _, out := range []string{
		"",
		"service backend exited with code 1",
		"yaml: line 4: did not find expected key",
	} {
		if got := composeFailure(out); got != "" {
			t.Errorf("composeFailure(%q) = %q, want no claim", out, got)
		}
	}
}

// A compose run can produce megabytes and only the end explains a failure.
func TestTailBufferKeepsTheEnd(t *testing.T) {
	tb := &tailBuffer{limit: 16}
	for i := 0; i < 100; i++ {
		if _, err := tb.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if len(tb.String()) != 16 {
		t.Fatalf("kept %d bytes, want 16", len(tb.String()))
	}
	if !strings.HasSuffix("0123456789", tb.String()[len(tb.String())-1:]) {
		t.Errorf("the tail is not the end of the stream: %q", tb.String())
	}
}
