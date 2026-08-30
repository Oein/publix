package deployspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Oein/publix/internal/compose"
)

// ComposeFilenames are the conventional compose file names, in the order
// docker itself resolves them.
var ComposeFilenames = []string{
	"compose.yaml", "compose.yml",
	"docker-compose.yaml", "docker-compose.yml",
}

// Detection is what publix inferred about a repository. It drives both
// auto-detection at deploy time and the deployment.yaml that the dashboard
// offers to write when a repo is imported.
type Detection struct {
	Kind Kind `json:"kind"`
	// Framework is a human label, e.g. "Next.js", used in the UI.
	Framework  string   `json:"framework,omitempty"`
	Dockerfile string   `json:"dockerfile,omitempty"`
	Compose    string   `json:"compose,omitempty"`
	Service    string   `json:"service,omitempty"`
	Port       int      `json:"port,omitempty"`
	Install    string   `json:"install,omitempty"`
	Command    string   `json:"command,omitempty"`
	Output     string   `json:"output,omitempty"`
	SPA        bool     `json:"spa,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

// Detect inspects a checkout and infers how it should be deployed.
//
// The precedence is deliberate: an explicit compose file describes a whole
// stack and outranks a Dockerfile, which in turn outranks a framework guess.
// A repository that ships either has already told us how it wants to run.
func Detect(root string) Detection {
	d := Detection{Kind: KindDockerfile}

	for _, name := range ComposeFilenames {
		if exists(filepath.Join(root, name)) {
			d.Kind = KindCompose
			d.Compose = name
			d.Framework = "Docker Compose"
			if svc, port := compose.Guess(filepath.Join(root, name)); svc != "" {
				d.Service, d.Port = svc, port
			}
			return d
		}
	}

	if exists(filepath.Join(root, "Dockerfile")) {
		d.Dockerfile = "Dockerfile"
		d.Framework = "Dockerfile"
		d.Port = dockerfilePort(filepath.Join(root, "Dockerfile"))
		if d.Port == 0 {
			d.Port = 3000
			d.Notes = append(d.Notes, "No EXPOSE found in the Dockerfile; assuming port 3000.")
		}
		return d
	}

	if js := detectNode(root); js != nil {
		return *js
	}

	if exists(filepath.Join(root, "go.mod")) {
		d.Framework = "Go"
		d.Port = 8080
		d.Notes = append(d.Notes, "No Dockerfile found. Add one, or set type: static.")
		return d
	}
	if exists(filepath.Join(root, "requirements.txt")) || exists(filepath.Join(root, "pyproject.toml")) {
		d.Framework = "Python"
		d.Port = 8000
		d.Notes = append(d.Notes, "No Dockerfile found. Add one so publix knows how to build this.")
		return d
	}
	if exists(filepath.Join(root, "index.html")) {
		return Detection{Kind: KindStatic, Framework: "Static HTML", Output: ".", Port: 80, SPA: false}
	}

	d.Notes = append(d.Notes, "Could not detect how to build this repository. Add a Dockerfile, a compose file, or a deployment.yaml.")
	return d
}

// nodeFramework maps a dependency to its build conventions.
type nodeFramework struct {
	dep     string
	name    string
	kind    Kind
	output  string
	port    int
	spa     bool
	command string
}

// Ordered most-specific first: a Next.js project also depends on react.
var nodeFrameworks = []nodeFramework{
	{dep: "next", name: "Next.js", kind: KindDockerfile, port: 3000},
	{dep: "nuxt", name: "Nuxt", kind: KindDockerfile, port: 3000},
	{dep: "@sveltejs/kit", name: "SvelteKit", kind: KindDockerfile, port: 3000},
	{dep: "@remix-run/serve", name: "Remix", kind: KindDockerfile, port: 3000},
	{dep: "astro", name: "Astro", kind: KindStatic, output: "dist", port: 80},
	{dep: "vite", name: "Vite", kind: KindStatic, output: "dist", port: 80, spa: true},
	{dep: "react-scripts", name: "Create React App", kind: KindStatic, output: "build", port: 80, spa: true},
	{dep: "@angular/cli", name: "Angular", kind: KindStatic, output: "dist", port: 80, spa: true},
	{dep: "vuepress", name: "VuePress", kind: KindStatic, output: ".vuepress/dist", port: 80},
	{dep: "express", name: "Express", kind: KindDockerfile, port: 3000},
	{dep: "fastify", name: "Fastify", kind: KindDockerfile, port: 3000},
}

// detectNode reads package.json and infers the framework's conventions.
func detectNode(root string) *Detection {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil
	}
	has := func(dep string) bool {
		_, a := pkg.Dependencies[dep]
		_, b := pkg.DevDependencies[dep]
		return a || b
	}

	d := Detection{Kind: KindStatic, Framework: "Node.js", Install: nodeInstall(root), Port: 80, Output: "dist"}
	for _, f := range nodeFrameworks {
		if !has(f.dep) {
			continue
		}
		d.Framework, d.Kind, d.SPA = f.name, f.kind, f.spa
		if f.output != "" {
			d.Output = f.output
		}
		if f.port != 0 {
			d.Port = f.port
		}
		break
	}

	if _, ok := pkg.Scripts["build"]; ok {
		d.Command = nodeRun(root) + " build"
	}
	if d.Kind == KindDockerfile {
		d.Notes = append(d.Notes,
			"A "+d.Framework+" server needs a Dockerfile. publix will generate one if the repository has none.")
		d.Output = ""
	}
	return &d
}

// nodeInstall picks the install command matching the repository's lockfile,
// which is the difference between a reproducible build and a lucky one.
func nodeInstall(root string) string {
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm install --frozen-lockfile"
	case exists(filepath.Join(root, "yarn.lock")):
		return "yarn install --frozen-lockfile"
	case exists(filepath.Join(root, "bun.lockb")), exists(filepath.Join(root, "bun.lock")):
		return "bun install --frozen-lockfile"
	case exists(filepath.Join(root, "package-lock.json")):
		return "npm ci"
	default:
		return "npm install"
	}
}

func nodeRun(root string) string {
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm run"
	case exists(filepath.Join(root, "yarn.lock")):
		return "yarn"
	case exists(filepath.Join(root, "bun.lockb")), exists(filepath.Join(root, "bun.lock")):
		return "bun run"
	default:
		return "npm run"
	}
}

// dockerfilePort reads the last EXPOSE directive, which is conventionally
// the port the final stage serves on.
func dockerfilePort(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	port := 0
	for _, line := range strings.Split(string(raw), "\n") {
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

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
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

// DetectFromPackageJSON infers a Node.js project's conventions from a
// package.json alone. The dashboard uses it to describe a repository before
// anything has been cloned.
func DetectFromPackageJSON(raw []byte) *Detection {
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil
	}
	has := func(dep string) bool {
		_, a := pkg.Dependencies[dep]
		_, b := pkg.DevDependencies[dep]
		return a || b
	}

	d := &Detection{Kind: KindStatic, Framework: "Node.js", Port: 80, Output: "dist", Install: "npm install"}
	for _, f := range nodeFrameworks {
		if !has(f.dep) {
			continue
		}
		d.Framework, d.Kind, d.SPA = f.name, f.kind, f.spa
		if f.output != "" {
			d.Output = f.output
		}
		if f.port != 0 {
			d.Port = f.port
		}
		break
	}
	if _, ok := pkg.Scripts["build"]; ok {
		d.Command = "npm run build"
	}
	if d.Kind == KindDockerfile {
		d.Output = ""
		d.Notes = append(d.Notes, "A "+d.Framework+" server needs a Dockerfile in the repository.")
	}
	return d
}
