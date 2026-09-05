package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

var (
	registryOnce sync.Once
	registryErr  error
)

// requiresPackageRegistry skips a test that cannot run without reaching a
// package registry from inside a container build.
//
// This is a real precondition, not a flake: a sandbox that intercepts TLS
// breaks npm inside `docker build` even though the host itself is online.
// Skipping with the reason is honest; failing would report a publix bug
// that is not there.
func requiresPackageRegistry(t *testing.T) {
	t.Helper()
	registryOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "node:22-alpine",
			"npm", "view", "next", "version")
		if out, err := cmd.CombinedOutput(); err != nil {
			registryErr = &registryUnreachable{output: strings.TrimSpace(string(out))}
		}
	})
	if registryErr != nil {
		t.Skipf("package registry unreachable from inside a container build: %v", registryErr)
	}
}

type registryUnreachable struct{ output string }

func (e *registryUnreachable) Error() string {
	if len(e.output) > 200 {
		return e.output[:200] + "…"
	}
	return e.output
}

// The whole point of the framework templates: a repository carrying nothing
// but its own source and config builds, starts and serves — no Dockerfile,
// and no deployment.yaml either.
func TestDeployNextJSWithoutDockerfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: builds a real Next.js app")
	}
	h := newHarness(t)
	requiresPackageRegistry(t)

	p := h.project("shopfront", map[string]string{
		"package.json": `{
  "name": "shopfront",
  "private": true,
  "scripts": { "build": "next build", "start": "next start" },
  "dependencies": { "next": "14.2.15", "react": "18.3.1", "react-dom": "18.3.1" }
}`,
		"next.config.js": "module.exports = { output: 'standalone' };\n",
		"app/layout.js": `export default function RootLayout({ children }) {
  return <html lang="en"><body>{children}</body></html>;
}`,
		"app/page.js": `export default function Page() {
  return <main>hello from next</main>;
}`,
	})

	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	if dep.Kind != "framework" {
		t.Errorf("kind = %q, want framework", dep.Kind)
	}

	ctx := context.Background()
	body := httpGet(t, h, h.oneContainer(ctx, p.ID), 3000, "/")
	if !strings.Contains(body, "hello from next") {
		t.Errorf("the app served %q, want the page content", body)
	}

	// The build log carries the generated Dockerfile, because it exists
	// nowhere else and is the first thing needed when one goes wrong.
	lines, err := h.logs.Read(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for _, l := range lines {
		log.WriteString(l.Text + "\n")
	}
	for _, want := range []string{"generated Dockerfile", "FROM node:22-alpine", ".next/standalone"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the build log should show %q", want)
		}
	}
}

// A Vite site with no Dockerfile builds inside Docker, so the host needs no
// Node toolchain at all.
func TestDeployViteStaticWithoutHostToolchain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: builds a real Vite app")
	}
	h := newHarness(t)
	requiresPackageRegistry(t)

	p := h.project("brochure", map[string]string{
		"package.json": `{
  "name": "brochure",
  "private": true,
  "type": "module",
  "scripts": { "build": "vite build" },
  "devDependencies": { "vite": "5.4.10" }
}`,
		"vite.config.js": "export default { build: { outDir: 'dist' } };\n",
		"index.html":     `<!doctype html><html><body><div id="app">hello from vite</div><script type="module" src="/main.js"></script></body></html>`,
		"main.js":        "console.log('ready');\n",
	})

	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	if dep.Kind != "static" {
		t.Errorf("kind = %q, want static", dep.Kind)
	}

	ctx := context.Background()
	body := httpGet(t, h, h.oneContainer(ctx, p.ID), 80, "/")
	if !strings.Contains(body, "hello from vite") {
		t.Errorf("the site served %q", body)
	}
}

// A Go service with no Dockerfile compiles to a static binary and runs.
func TestDeployGoWithoutDockerfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: builds a real Go binary")
	}
	h := newHarness(t)

	p := h.project("pinger", map[string]string{
		"go.mod": "module example.com/pinger\n\ngo 1.24\n",
		"main.go": `package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from go")
	})
	http.ListenAndServe(":"+port, nil)
}
`,
	})

	dep := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(dep)

	ctx := context.Background()
	if body := httpGet(t, h, h.oneContainer(ctx, p.ID), 8080, "/"); !strings.Contains(body, "hello from go") {
		t.Errorf("the service served %q", body)
	}
}

// A project publix cannot build must fail with a message that says what to
// set, not a container that dies deep in a build.
func TestUnbuildableProjectFailsWithGuidance(t *testing.T) {
	h := newHarness(t)
	p := h.project("mystery", map[string]string{"README.md": "# nothing here\n"})

	dep := h.deploy(p, Options{Trigger: "test"})
	if dep.Status != store.StatusFailed {
		t.Fatalf("deploy ended %s, want failed", dep.Status)
	}
	if !strings.Contains(dep.Error, "build.start") && !strings.Contains(dep.Error, "could not") {
		t.Errorf("the error should explain what to do:\n%s", dep.Error)
	}

	// Nothing should have been left running.
	containers, err := h.docker.ListContainers(context.Background(), true, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 0 {
		t.Errorf("a failed detection left %d containers behind", len(containers))
	}
}

// A shared volume gives every project the same directory. This is the
// opposite guarantee from a project-scoped one, so it is worth proving
// against real containers rather than trusting the path arithmetic.
func TestSharedScopeVolumeIsVisibleToEveryProject(t *testing.T) {
	h := newHarness(t)

	shared := filepath.Join(h.home, "media")
	perProject := filepath.Join(h.home, "data")
	for _, d := range []string{shared, perProject} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.store.SetSettings(func(s *store.Settings) error {
		s.Volumes = []store.Volume{
			{Name: "media", Path: shared, Scope: store.ScopeShared},
			{Name: "data", Path: perProject, Scope: store.ScopeProject},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	spec := "port: 80\nvolumes: [media, data]\nrelease:\n  drain: 0s\n"
	dockerfile := "FROM nginx:alpine\n"
	a := h.project("alpha", map[string]string{"deployment.yaml": spec, "Dockerfile": dockerfile})
	b := h.project("beta", map[string]string{"deployment.yaml": spec, "Dockerfile": dockerfile})

	h.mustSucceed(h.deploy(a, Options{Trigger: "test"}))
	h.mustSucceed(h.deploy(b, Options{Trigger: "test"}))

	ctx := context.Background()
	alphaC := h.oneContainer(ctx, a.ID)
	betaC := h.oneContainer(ctx, b.ID)

	// Written to the shared volume by one project, readable by the other.
	if _, err := h.docker.Exec(ctx, alphaC, []string{"sh", "-c", "echo from-alpha > /shared/media/note"}); err != nil {
		t.Fatal(err)
	}
	res, err := h.docker.Exec(ctx, betaC, []string{"sh", "-c", "cat /shared/media/note 2>&1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "from-alpha") {
		t.Errorf("beta cannot read alpha's file on the shared volume: %q", res.Stdout+res.Stderr)
	}

	// The shared directory is the volume root itself, with no project
	// subdirectory in the way.
	if _, err := os.Stat(filepath.Join(shared, "note")); err != nil {
		t.Errorf("the shared volume should hold the file at its root: %v", err)
	}

	// And the project-scoped volume alongside it still isolates.
	if _, err := h.docker.Exec(ctx, alphaC, []string{"sh", "-c", "echo private > /shared/data/secret"}); err != nil {
		t.Fatal(err)
	}
	res, err = h.docker.Exec(ctx, betaC, []string{"sh", "-c", "ls /shared/data"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Stdout, "secret") {
		t.Errorf("beta can see alpha's file on a project-scoped volume: %q", res.Stdout)
	}
	if _, err := os.Stat(filepath.Join(perProject, a.ID, "secret")); err != nil {
		t.Errorf("the project-scoped file should be under the project's own directory: %v", err)
	}
}
