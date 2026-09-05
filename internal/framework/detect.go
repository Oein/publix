package framework

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Mode is how a detected project is served.
type Mode string

const (
	// ModeServer runs a long-lived process.
	ModeServer Mode = "server"
	// ModeStatic builds files and serves them from a web server.
	ModeStatic Mode = "static"
	// ModeUnknown means publix could not work out how to build it.
	ModeUnknown Mode = "unknown"
)

// Result is what publix worked out about a repository.
type Result struct {
	// ID is the stable framework identifier, e.g. "nextjs".
	ID string `json:"id,omitempty"`
	// Name is the human label shown in the dashboard.
	Name string `json:"name"`
	// Mode says whether this runs a server or produces static files.
	Mode Mode `json:"mode"`

	// Port the application listens on.
	Port int `json:"port,omitempty"`

	// Install, Build and Start are the commands the generated Dockerfile
	// runs. Any of them may be empty when the framework does not need it.
	Install string `json:"install,omitempty"`
	Build   string `json:"build,omitempty"`
	Start   string `json:"start,omitempty"`

	// Output is the directory a static build emits.
	Output string `json:"output,omitempty"`
	// SPA rewrites unknown paths to index.html.
	SPA bool `json:"spa,omitempty"`

	// Standalone marks a Next.js project configured for standalone
	// output, which yields a runtime image an order of magnitude smaller.
	Standalone bool `json:"standalone,omitempty"`

	// Builder and Runtime are the images used to build and to run.
	Builder string `json:"builder,omitempty"`
	Runtime string `json:"runtime,omitempty"`

	// ConfigFile is the file that identified the framework, so the
	// dashboard can say *why* it thinks what it thinks.
	ConfigFile string `json:"configFile,omitempty"`

	// Dockerfile names a Dockerfile already in the repository. When set,
	// publix defers to it and generates nothing.
	Dockerfile string `json:"dockerfile,omitempty"`
	// Compose names a compose file already in the repository.
	Compose string `json:"compose,omitempty"`
	// Service is the compose service that should receive the domains.
	Service string `json:"service,omitempty"`

	// Notes explain anything the user should know before deploying.
	Notes []string `json:"notes,omitempty"`

	// Node is the detected JavaScript toolchain, when there is one.
	Node *Node `json:"-"`
}

// Generated reports whether publix will write the Dockerfile itself.
func (r *Result) Generated() bool {
	return r != nil && r.Dockerfile == "" && r.Compose == "" && r.Mode != ModeUnknown
}

// Detect works out what a repository is and how to build it.
//
// The order is deliberate. A compose file or a Dockerfile is an explicit
// statement by the repository's authors about how it runs, and outranks
// anything publix might infer. Only when neither is present does it fall
// through to reading framework config files — and that is where most
// repositories land, because most repositories never wrote either.
func Detect(src Source) *Result {
	if c := firstExisting(src,
		"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
	); c != "" {
		return &Result{Name: "Docker Compose", Mode: ModeServer, Compose: c}
	}

	if src.Exists("Dockerfile") {
		r := &Result{Name: "Dockerfile", Mode: ModeServer, Dockerfile: "Dockerfile", Port: dockerfilePort(src)}
		if r.Port == 0 {
			r.Port = 3000
			r.Notes = append(r.Notes, "The Dockerfile has no EXPOSE line, so publix assumed port 3000. Set `port:` in deployment.yaml if that is wrong.")
		}
		return r
	}

	if node := detectNode(src); node != nil {
		return detectJavaScript(src, node)
	}
	if r := detectGo(src); r != nil {
		return r
	}
	if r := detectPython(src); r != nil {
		return r
	}
	if src.Exists("index.html") {
		return &Result{
			Name: "Static site", Mode: ModeStatic, Output: ".", Port: 80,
			Runtime: staticRuntime,
		}
	}

	return &Result{
		Name: "Unknown", Mode: ModeUnknown,
		Notes: []string{"publix could not work out how to build this repository. Add a Dockerfile, a compose file, or a deployment.yaml describing the build."},
	}
}

// detectJavaScript identifies a Node project and, crucially, reads its
// framework config file to learn how it is meant to be served. Whether an
// Astro or SvelteKit project is a static site or a server is a decision
// made in that file and nowhere else.
func detectJavaScript(src Source, node *Node) *Result {
	r := &Result{
		Node:    node,
		Install: node.Install,
		Builder: node.BuilderImage(),
		Runtime: node.BuilderImage(),
	}

	switch {
	case node.Has("next"):
		cfg := findConfig(src, "next.config")
		text := readText(src, cfg)
		r.ID, r.Name, r.Mode, r.Port = "nextjs", "Next.js", ModeServer, 3000
		r.ConfigFile = cfg
		r.Build = buildScript(node, "next build")
		r.Start = startScript(node, "next start -p 3000")
		// Standalone output bundles only what the server needs, turning a
		// gigabyte-scale runtime image into a small one. It is opt-in, so
		// the config file is the only way to know.
		r.Standalone = hasStandaloneOutput(text)
		if isStaticExport(text, node) {
			r.Mode, r.Port, r.Output, r.Start = ModeStatic, 80, "out", ""
			r.Runtime = staticRuntime
			r.Notes = append(r.Notes, "This Next.js app is configured for static export, so it is served as static files.")
		}

	case node.Has("nuxt"):
		cfg := findConfig(src, "nuxt.config")
		text := readText(src, cfg)
		r.ID, r.Name, r.Mode, r.Port = "nuxt", "Nuxt", ModeServer, 3000
		r.ConfigFile = cfg
		r.Build = buildScript(node, "nuxt build")
		r.Start = "node .output/server/index.mjs"
		if reSSRFalse.MatchString(text) || reNuxtGenerate.MatchString(text) {
			r.Mode, r.Port, r.Output, r.Start = ModeStatic, 80, ".output/public", ""
			r.Runtime = staticRuntime
			r.Build = buildScript(node, "nuxt generate")
			r.Notes = append(r.Notes, "This Nuxt app has ssr disabled, so it is pre-rendered and served as static files.")
		}

	case node.Has("@sveltejs/kit"):
		cfg := findConfig(src, "svelte.config")
		text := readText(src, cfg)
		r.ID, r.Name, r.ConfigFile = "sveltekit", "SvelteKit", cfg
		r.Build = buildScript(node, "vite build")
		// SvelteKit's adapter decides everything: adapter-static produces
		// files, adapter-node produces a server. Guessing without reading
		// the config would be wrong half the time.
		switch {
		case strings.Contains(text, "adapter-static") || node.Has("@sveltejs/adapter-static"):
			r.Mode, r.Port, r.Output = ModeStatic, 80, "build"
			r.Runtime, r.SPA = staticRuntime, true
		case strings.Contains(text, "adapter-node") || node.Has("@sveltejs/adapter-node"):
			r.Mode, r.Port, r.Start = ModeServer, 3000, "node build/index.js"
		default:
			r.Mode, r.Port, r.Start = ModeServer, 3000, "node build/index.js"
			r.Notes = append(r.Notes, "No SvelteKit adapter was recognised. publix assumed adapter-node; install @sveltejs/adapter-node, or set build.start in deployment.yaml.")
		}

	case node.Has("@remix-run/dev") || node.Has("@react-router/dev"):
		r.ID, r.Name, r.Mode, r.Port = "remix", "Remix", ModeServer, 3000
		r.ConfigFile = findConfig(src, "vite.config")
		r.Build = buildScript(node, "remix vite:build")
		r.Start = startScript(node, "remix-serve ./build/server/index.js")

	case node.Has("astro"):
		cfg := findConfig(src, "astro.config")
		text := readText(src, cfg)
		r.ID, r.Name, r.ConfigFile = "astro", "Astro", cfg
		r.Build = buildScript(node, "astro build")
		if reAstroServer.MatchString(text) {
			r.Mode, r.Port, r.Start = ModeServer, 4321, "node ./dist/server/entry.mjs"
			r.Notes = append(r.Notes, "This Astro project has server output enabled, so it runs as a server rather than static files.")
		} else {
			r.Mode, r.Port, r.Output, r.Runtime = ModeStatic, 80, "dist", staticRuntime
		}

	case node.Has("@nestjs/core"):
		r.ID, r.Name, r.Mode, r.Port = "nestjs", "NestJS", ModeServer, 3000
		r.Build = buildScript(node, "nest build")
		r.Start = startScript(node, "node dist/main.js")

	case node.Has("gatsby"):
		r.ID, r.Name, r.Mode, r.Port = "gatsby", "Gatsby", ModeStatic, 80
		r.ConfigFile = findConfig(src, "gatsby-config")
		r.Build, r.Output, r.Runtime = buildScript(node, "gatsby build"), "public", staticRuntime

	case node.Has("@docusaurus/core"):
		r.ID, r.Name, r.Mode, r.Port = "docusaurus", "Docusaurus", ModeStatic, 80
		r.ConfigFile = findConfig(src, "docusaurus.config")
		r.Build, r.Output, r.Runtime = buildScript(node, "docusaurus build"), "build", staticRuntime

	case node.Has("@angular/cli"), src.Exists("angular.json"):
		r.ID, r.Name, r.Mode, r.Port = "angular", "Angular", ModeStatic, 80
		r.ConfigFile = "angular.json"
		r.Build, r.Output, r.Runtime, r.SPA = buildScript(node, "ng build"), angularOutput(src), staticRuntime, true

	case node.Has("react-scripts"):
		r.ID, r.Name, r.Mode, r.Port = "cra", "Create React App", ModeStatic, 80
		r.Build, r.Output, r.Runtime, r.SPA = buildScript(node, "react-scripts build"), "build", staticRuntime, true

	case node.Has("vite"):
		cfg := findConfig(src, "vite.config")
		r.ID, r.Name, r.Mode, r.Port = "vite", "Vite", ModeStatic, 80
		r.ConfigFile = cfg
		r.Build, r.Output, r.Runtime, r.SPA = buildScript(node, "vite build"), viteOutput(readText(src, cfg)), staticRuntime, true

	default:
		// A plain Node server: Express, Fastify, Hono, or something
		// hand-rolled. The start script is the only reliable signal.
		r.ID, r.Name, r.Mode, r.Port = "node", "Node.js", ModeServer, 3000
		r.Build = optionalBuildScript(node)
		r.Start = nodeStart(node)
		if r.Start == "" {
			r.Mode = ModeUnknown
			r.Notes = append(r.Notes, "This looks like a Node project, but it has no start script and no main entry point. Add one, or set build.start in deployment.yaml.")
		}
	}

	if r.Node == nil {
		r.Node = node
	}
	return r
}

// staticRuntime serves a built static site.
const staticRuntime = "nginx:1.27-alpine"

// buildScript prefers the project's own build script over the framework's
// default command: a repository that wrote `"build": "next build && ..."`
// meant the `&& ...` part too.
func buildScript(n *Node, fallback string) string {
	if n.Script("build") != "" {
		return n.RunScript("build")
	}
	return "npx --no-install " + fallback
}

// optionalBuildScript returns the build script only if one exists, since a
// plain Node server usually has nothing to build.
func optionalBuildScript(n *Node) string {
	if n.Script("build") != "" {
		return n.RunScript("build")
	}
	return ""
}

// startScript prefers the project's own start script.
func startScript(n *Node, fallback string) string {
	if n.Script("start") != "" {
		return n.RunScript("start")
	}
	return "npx --no-install " + fallback
}

// nodeStart works out how to run a plain Node server.
func nodeStart(n *Node) string {
	if n.Script("start") != "" {
		return n.RunScript("start")
	}
	if n.Main != "" {
		return "node " + n.Main
	}
	return ""
}

// Config-file patterns. These are read as text rather than evaluated: a
// config file is JavaScript and may do anything, so publix looks for the
// declaration and treats anything ambiguous as the safer default.
var (
	reStandalone   = regexp.MustCompile(`output\s*:\s*['"\x60]standalone['"\x60]`)
	reNextExport   = regexp.MustCompile(`output\s*:\s*['"\x60]export['"\x60]`)
	reSSRFalse     = regexp.MustCompile(`ssr\s*:\s*false`)
	reNuxtGenerate = regexp.MustCompile(`nitro\s*:\s*\{[^}]*preset\s*:\s*['"\x60]static['"\x60]`)
	reAstroServer  = regexp.MustCompile(`output\s*:\s*['"\x60](server|hybrid)['"\x60]`)
	reViteOutDir   = regexp.MustCompile(`outDir\s*:\s*['"\x60]([^'"\x60]+)['"\x60]`)
)

func hasStandaloneOutput(text string) bool { return reStandalone.MatchString(text) }

// isStaticExport reports whether a Next.js project produces static files.
func isStaticExport(text string, n *Node) bool {
	if reNextExport.MatchString(text) {
		return true
	}
	// Before Next 13 static export was a separate script rather than a
	// config option.
	return strings.Contains(n.Script("build"), "next export")
}

// viteOutput reads a custom outDir from a Vite config.
func viteOutput(text string) string {
	if m := reViteOutDir.FindStringSubmatch(text); len(m) == 2 {
		return strings.TrimPrefix(strings.TrimSuffix(m[1], "/"), "./")
	}
	return "dist"
}

// angularOutput reads the build output path from angular.json, which is
// where Angular records it rather than in a config file.
func angularOutput(src Source) string {
	raw, err := src.Read("angular.json")
	if err != nil {
		return "dist"
	}
	var cfg struct {
		Projects map[string]struct {
			Architect map[string]struct {
				Options struct {
					OutputPath string `json:"outputPath"`
				} `json:"options"`
			} `json:"architect"`
		} `json:"projects"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return "dist"
	}
	for _, project := range cfg.Projects {
		if b, ok := project.Architect["build"]; ok && b.Options.OutputPath != "" {
			// Angular 17+ nests the browser bundle one level deeper.
			return strings.TrimPrefix(b.Options.OutputPath, "./")
		}
	}
	return "dist"
}

// dockerfilePort reads the last EXPOSE, conventionally the port the final
// stage serves on.
func dockerfilePort(src Source) int {
	text := readText(src, "Dockerfile")
	port := 0
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && strings.EqualFold(f[0], "EXPOSE") {
			p := f[1]
			if i := strings.Index(p, "/"); i > 0 {
				p = p[:i]
			}
			if n := atoi(p); n > 0 {
				port = n
			}
		}
	}
	return port
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
