package framework

import (
	"strings"
	"testing"
)

// fake builds a Source from an in-memory file set.
func fake(files map[string]string) Source {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	return NewFiles(paths, func(p string) ([]byte, error) {
		return []byte(files[p]), nil
	})
}

const nextPkg = `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"14.2.0","react":"18"}}`

// The headline case: a repository with a Next.js config and no Dockerfile
// must be recognised and buildable.
func TestDetectNextJS(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json":      nextPkg,
		"package-lock.json": "{}",
		"next.config.js":    "module.exports = { reactStrictMode: true }",
	}))

	if r.ID != "nextjs" {
		t.Fatalf("id = %q, want nextjs", r.ID)
	}
	if r.Mode != ModeServer {
		t.Errorf("mode = %q, want server", r.Mode)
	}
	if r.Port != 3000 {
		t.Errorf("port = %d, want 3000", r.Port)
	}
	if !r.Generated() {
		t.Error("publix should generate a Dockerfile for this repository")
	}
	if r.ConfigFile != "next.config.js" {
		t.Errorf("configFile = %q, want next.config.js", r.ConfigFile)
	}
	if r.Standalone {
		t.Error("standalone should be off when the config does not ask for it")
	}
	// The project's own scripts win: `"build": "next build"` may have been
	// customised, and running the framework default would ignore that.
	if r.Build != "npm run build" || r.Start != "npm run start" {
		t.Errorf("build=%q start=%q, want the project's own scripts", r.Build, r.Start)
	}
}

// Reading the config file is the only way to know about standalone output,
// and it changes the generated image completely.
func TestNextStandaloneFromConfig(t *testing.T) {
	for _, cfg := range []string{
		`module.exports = { output: 'standalone' }`,
		"export default { output: \"standalone\" }",
		"const c = {\n  output: `standalone`,\n}\nexport default c",
	} {
		r := Detect(fake(map[string]string{
			"package.json": nextPkg, "next.config.mjs": cfg,
		}))
		if !r.Standalone {
			t.Errorf("standalone not detected in: %s", cfg)
		}
	}

	r := Detect(fake(map[string]string{"package.json": nextPkg, "next.config.js": "module.exports = {}"}))
	if r.Standalone {
		t.Error("standalone should not be inferred from an empty config")
	}
}

// A Next.js app configured for static export is a static site, not a server.
func TestNextStaticExport(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json":   nextPkg,
		"next.config.js": `module.exports = { output: "export" }`,
	}))
	if r.Mode != ModeStatic {
		t.Fatalf("mode = %q, want static", r.Mode)
	}
	if r.Output != "out" {
		t.Errorf("output = %q, want out", r.Output)
	}
	if r.Port != 80 {
		t.Errorf("port = %d, want 80", r.Port)
	}
}

// SvelteKit's adapter decides server versus static, and it lives in the
// config file. Guessing without reading it would be wrong half the time.
func TestSvelteKitAdapterDecidesMode(t *testing.T) {
	pkg := `{"devDependencies":{"@sveltejs/kit":"2.0.0","vite":"5"},"scripts":{"build":"vite build"}}`

	static := Detect(fake(map[string]string{
		"package.json":     pkg,
		"svelte.config.js": `import adapter from '@sveltejs/adapter-static'; export default { kit: { adapter: adapter() } }`,
	}))
	if static.Mode != ModeStatic {
		t.Errorf("adapter-static should give a static site, got %q", static.Mode)
	}
	if static.Output != "build" {
		t.Errorf("output = %q, want build", static.Output)
	}

	server := Detect(fake(map[string]string{
		"package.json":     pkg,
		"svelte.config.js": `import adapter from '@sveltejs/adapter-node'; export default { kit: { adapter: adapter() } }`,
	}))
	if server.Mode != ModeServer {
		t.Errorf("adapter-node should give a server, got %q", server.Mode)
	}
	if server.Start != "node build/index.js" {
		t.Errorf("start = %q", server.Start)
	}
}

func TestAstroServerOutput(t *testing.T) {
	pkg := `{"dependencies":{"astro":"4"},"scripts":{"build":"astro build"}}`

	static := Detect(fake(map[string]string{"package.json": pkg, "astro.config.mjs": "export default {}"}))
	if static.Mode != ModeStatic {
		t.Errorf("Astro defaults to static, got %q", static.Mode)
	}

	ssr := Detect(fake(map[string]string{
		"package.json": pkg, "astro.config.mjs": `export default { output: 'server' }`,
	}))
	if ssr.Mode != ModeServer {
		t.Errorf("output: server should give a server, got %q", ssr.Mode)
	}
}

func TestNuxtSSRDisabledBecomesStatic(t *testing.T) {
	pkg := `{"dependencies":{"nuxt":"3"},"scripts":{"build":"nuxt build"}}`

	server := Detect(fake(map[string]string{"package.json": pkg, "nuxt.config.ts": "export default defineNuxtConfig({})"}))
	if server.Mode != ModeServer {
		t.Errorf("Nuxt defaults to a server, got %q", server.Mode)
	}

	static := Detect(fake(map[string]string{
		"package.json": pkg, "nuxt.config.ts": "export default defineNuxtConfig({ ssr: false })",
	}))
	if static.Mode != ModeStatic {
		t.Errorf("ssr: false should give static, got %q", static.Mode)
	}
}

// The lockfile picks the package manager: installing a pnpm project with
// npm produces a different dependency tree than the author tested.
func TestPackageManagerFromLockfile(t *testing.T) {
	pkg := `{"dependencies":{"next":"14"},"scripts":{"build":"next build","start":"next start"}}`
	cases := map[string]struct{ manager, install string }{
		"pnpm-lock.yaml":    {"pnpm", "pnpm install --frozen-lockfile"},
		"yarn.lock":         {"yarn", "yarn install --frozen-lockfile"},
		"bun.lockb":         {"bun", "bun install --frozen-lockfile"},
		"package-lock.json": {"npm", "npm ci"},
	}
	for lockfile, want := range cases {
		r := Detect(fake(map[string]string{"package.json": pkg, lockfile: ""}))
		if r.Node.Manager != want.manager {
			t.Errorf("%s: manager = %q, want %q", lockfile, r.Node.Manager, want.manager)
		}
		if r.Install != want.install {
			t.Errorf("%s: install = %q, want %q", lockfile, r.Install, want.install)
		}
	}

	// With no lockfile, a frozen install would fail outright.
	none := Detect(fake(map[string]string{"package.json": pkg}))
	if strings.Contains(none.Install, "frozen") || none.Install == "npm ci" {
		t.Errorf("without a lockfile the install must not be frozen, got %q", none.Install)
	}
}

func TestNodeVersionFromEngines(t *testing.T) {
	cases := map[string]string{
		`{"engines":{"node":">=20"}}`:   "20",
		`{"engines":{"node":"22.1.0"}}`: "22",
		`{"engines":{"node":"^18"}}`:    "18",
		`{}`:                            DefaultNodeVersion,
		// Node 16 is out of support; build on the default rather than pull
		// an image that no longer receives fixes.
		`{"engines":{"node":"16"}}`: DefaultNodeVersion,
	}
	for pkg, want := range cases {
		n := detectNode(fake(map[string]string{"package.json": pkg}))
		if n == nil {
			t.Fatalf("no node detected for %s", pkg)
		}
		if n.Version != want {
			t.Errorf("%s: version = %q, want %q", pkg, n.Version, want)
		}
	}

	nvmrc := detectNode(fake(map[string]string{"package.json": "{}", ".nvmrc": "20.11.0\n"}))
	if nvmrc.Version != "20" {
		t.Errorf(".nvmrc should be honoured, got %q", nvmrc.Version)
	}
}

// An explicit Dockerfile or compose file outranks any inference: the
// repository has already said how it runs.
func TestExplicitFilesWinOverDetection(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json": nextPkg,
		"Dockerfile":   "FROM node:22\nEXPOSE 4000\n",
	}))
	if r.Dockerfile != "Dockerfile" || r.Generated() {
		t.Errorf("a repository's own Dockerfile must win: %+v", r)
	}
	if r.Port != 4000 {
		t.Errorf("port = %d, want the Dockerfile's EXPOSE", r.Port)
	}

	c := Detect(fake(map[string]string{
		"package.json": nextPkg, "Dockerfile": "FROM node\n", "compose.yaml": "services:\n  web:\n    build: .\n",
	}))
	if c.Compose != "compose.yaml" {
		t.Errorf("a compose file must outrank a Dockerfile, got %+v", c)
	}
}

func TestDetectGo(t *testing.T) {
	r := Detect(fake(map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.23\n",
		"main.go": "package main\nfunc main() {}\n",
	}))
	if r.ID != "go" || r.Mode != ModeServer {
		t.Fatalf("got %+v", r)
	}
	if !strings.Contains(r.Builder, "1.23") {
		t.Errorf("builder = %q, should honour the go directive", r.Builder)
	}
	if !strings.Contains(r.Build, "-o /out/server .") {
		t.Errorf("build = %q, should target the module root", r.Build)
	}

	cmd := Detect(fake(map[string]string{
		"go.mod": "module x\ngo 1.24\n", "cmd/server/main.go": "package main",
	}))
	if !strings.Contains(cmd.Build, "./cmd/server") {
		t.Errorf("build = %q, should target cmd/server", cmd.Build)
	}
}

func TestDetectPython(t *testing.T) {
	fastapi := Detect(fake(map[string]string{
		"requirements.txt": "fastapi==0.110\nuvicorn[standard]\n", "main.py": "app = 1",
	}))
	if fastapi.ID != "fastapi" {
		t.Fatalf("id = %q, want fastapi", fastapi.ID)
	}
	if !strings.Contains(fastapi.Start, "uvicorn main:app") {
		t.Errorf("start = %q", fastapi.Start)
	}

	// A dependency the project did not declare must be installed, or the
	// generated image would start a server that is not there.
	flask := Detect(fake(map[string]string{"requirements.txt": "flask\n", "app.py": ""}))
	if !strings.Contains(flask.Install, "gunicorn") {
		t.Errorf("install = %q, should add the missing server", flask.Install)
	}
}

// A project publix cannot serve must say so rather than generate a
// Dockerfile that fails deep inside a build.
func TestUnknownProjectIsHonest(t *testing.T) {
	r := Detect(fake(map[string]string{"README.md": "hello"}))
	if r.Mode != ModeUnknown {
		t.Errorf("mode = %q, want unknown", r.Mode)
	}
	if len(r.Notes) == 0 {
		t.Error("an unknown project should explain what to do next")
	}
	if _, err := r.Render(); err == nil {
		t.Error("rendering a Dockerfile for an unknown project should fail")
	}
}

// The generated Dockerfiles are the product here, so their shape is
// asserted rather than merely "it produced something".
func TestRenderNextStandalone(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json":      nextPkg,
		"package-lock.json": "{}",
		"next.config.js":    `module.exports = { output: 'standalone' }`,
	}))
	out, err := r.Render()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, want := range []string{
		"FROM node:22-alpine AS deps",
		"COPY package.json package-lock.json*",
		"RUN npm ci",
		".next/standalone",
		".next/static",
		"USER node",
		"EXPOSE 3000",
		`CMD ["sh", "-c", "node server.js"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated Dockerfile is missing %q:\n%s", want, got)
		}
	}
	// Standalone output carries its own node_modules; reinstalling would
	// undo the whole point of it.
	if strings.Contains(got, "npm ci --omit=dev") {
		t.Errorf("standalone build should not reinstall dependencies:\n%s", got)
	}
}

func TestRenderNextNonStandalone(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json": nextPkg, "package-lock.json": "{}", "next.config.js": "module.exports = {}",
	}))
	out, _ := r.Render()
	got := string(out)

	// Without standalone output the runtime stage must drop dev
	// dependencies itself, or the image carries the whole build toolchain.
	if !strings.Contains(got, "npm ci --omit=dev") {
		t.Errorf("expected a production-only install:\n%s", got)
	}
	if !strings.Contains(got, `CMD ["sh", "-c", "npm run start"]`) {
		t.Errorf("expected the project's start script:\n%s", got)
	}
}

func TestRenderStaticUsesDockerNotTheHost(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json":   `{"devDependencies":{"vite":"5"},"scripts":{"build":"vite build"}}`,
		"pnpm-lock.yaml": "",
		"vite.config.ts": "export default {}",
	}))
	out, err := r.Render()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	for _, want := range []string{
		"corepack enable",
		"pnpm install --frozen-lockfile",
		"RUN pnpm run build",
		"FROM nginx:1.27-alpine",
		"COPY --from=build /app/dist /usr/share/nginx/html",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// SPA fallback belongs in the nginx config, not the Dockerfile.
	if !strings.Contains(string(r.NginxConf()), "try_files $uri $uri/ /index.html") {
		t.Errorf("a Vite SPA needs an index.html fallback:\n%s", r.NginxConf())
	}
}

func TestRenderGoAndPython(t *testing.T) {
	goResult := Detect(fake(map[string]string{"go.mod": "module x\ngo 1.24\n", "main.go": ""}))
	out, err := goResult.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM golang:1.24-alpine", "CGO_ENABLED=0", "COPY --from=build /out/server",
		"ca-certificates.crt", "USER 10001:10001",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("go Dockerfile missing %q:\n%s", want, out)
		}
	}

	py := Detect(fake(map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": ""}))
	out, err = py.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"python -m venv /opt/venv", "COPY --from=build /opt/venv /opt/venv", "uvicorn main:app", "USER app"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("python Dockerfile missing %q:\n%s", want, out)
		}
	}
}

// deployment.yaml has to be able to override anything publix inferred.
func TestOverridesApply(t *testing.T) {
	r := Detect(fake(map[string]string{"package.json": nextPkg, "next.config.js": ""}))
	spa := true
	o := r.Apply(Overrides{
		Install: "pnpm i", Build: "pnpm build", Start: "node custom.js",
		Builder: "node:20-alpine", Port: 4000, SPA: &spa,
	})

	if o.Install != "pnpm i" || o.Build != "pnpm build" || o.Start != "node custom.js" {
		t.Errorf("overrides not applied: %+v", o)
	}
	if o.Port != 4000 || o.Builder != "node:20-alpine" {
		t.Errorf("overrides not applied: %+v", o)
	}
	// The original must be untouched, since detection is shown separately
	// in the UI from what will actually run.
	if r.Start == "node custom.js" {
		t.Error("Apply mutated the detection result")
	}
}

// An explicit start command means a server even if detection said static:
// it is the clearest statement a user can make about how this runs.
func TestExplicitStartForcesServerMode(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json": `{"devDependencies":{"vite":"5"}}`, "vite.config.js": "",
	}))
	if r.Mode != ModeStatic {
		t.Fatalf("expected static, got %q", r.Mode)
	}
	o := r.Apply(Overrides{Start: "node server.js"})
	if o.Mode != ModeServer {
		t.Errorf("an explicit start command should switch to server mode, got %q", o.Mode)
	}
}

// A generated Dockerfile must not depend on reaching a distro package
// mirror. A build that works on one network and fails on another is worse
// than one that never worked, because it fails only in production.
func TestTemplatesDoNotInstallSystemPackages(t *testing.T) {
	cases := map[string]map[string]string{
		"go":      {"go.mod": "module x\ngo 1.24\n", "main.go": ""},
		"nextjs":  {"package.json": nextPkg, "package-lock.json": "{}", "next.config.js": ""},
		"vite":    {"package.json": `{"devDependencies":{"vite":"5"},"scripts":{"build":"vite build"}}`, "vite.config.js": ""},
		"fastapi": {"requirements.txt": "fastapi\nuvicorn\n", "main.py": ""},
	}
	for name, files := range cases {
		out, err := Detect(fake(files)).Render()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, forbidden := range []string{"apk add", "apt-get", "curl -fsSL", "yum install"} {
			if strings.Contains(string(out), forbidden) {
				t.Errorf("%s template runs %q, which needs a package mirror at build time:\n%s", name, forbidden, out)
			}
		}
	}
}

// Every generated Dockerfile should drop privileges: a container running as
// root is one container escape away from the host.
func TestTemplatesDropRoot(t *testing.T) {
	cases := map[string]map[string]string{
		"go":      {"go.mod": "module x\ngo 1.24\n", "main.go": ""},
		"nextjs":  {"package.json": nextPkg, "package-lock.json": "{}", "next.config.js": ""},
		"fastapi": {"requirements.txt": "fastapi\nuvicorn\n", "main.py": ""},
	}
	for name, files := range cases {
		out, err := Detect(fake(files)).Render()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(out), "USER ") {
			t.Errorf("%s template never drops root:\n%s", name, out)
		}
	}
}

func TestBunUsesItsOwnImage(t *testing.T) {
	r := Detect(fake(map[string]string{
		"package.json": `{"dependencies":{"next":"14"},"scripts":{"build":"next build","start":"next start"}}`,
		"bun.lockb":    "",
	}))
	if !strings.Contains(r.Builder, "oven/bun") {
		t.Errorf("builder = %q, want the official bun image", r.Builder)
	}
	out, err := r.Render()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "bun.sh/install") {
		t.Errorf("bun should not be installed by curl:\n%s", out)
	}
}
