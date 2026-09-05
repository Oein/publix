package framework

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Node describes the JavaScript toolchain a repository expects.
type Node struct {
	// Manager is npm, pnpm, yarn or bun.
	Manager string
	// Lockfile is the file that selected the manager, copied separately in
	// the Dockerfile so a dependency layer caches independently of source.
	Lockfile string
	// Install is the reproducible install command.
	Install string
	// Run prefixes a package.json script, e.g. "npm run".
	Run string
	// Version is the major Node version to build and run on.
	Version string
	// Scripts are the package.json scripts, so a template can prefer a
	// script the project actually defines.
	Scripts map[string]string
	// Main is package.json's entry point, used by the generic Node server
	// template when there is no start script.
	Main string
	// Dependencies and DevDependencies as declared.
	Dependencies    map[string]string
	DevDependencies map[string]string
	// Type is package.json's "type" field ("module" or "commonjs").
	Type string
}

// Has reports whether a dependency is declared, in either set.
func (n *Node) Has(dep string) bool {
	if n == nil {
		return false
	}
	if _, ok := n.Dependencies[dep]; ok {
		return true
	}
	_, ok := n.DevDependencies[dep]
	return ok
}

// Script returns a package.json script if it exists.
func (n *Node) Script(name string) string {
	if n == nil {
		return ""
	}
	return n.Scripts[name]
}

// RunScript renders the command to run a package.json script.
func (n *Node) RunScript(name string) string { return n.Run + " " + name }

// DefaultNodeVersion is used when a repository does not pin one. It tracks
// the current LTS, which is what a project that never said gets by default
// on every other platform too.
const DefaultNodeVersion = "22"

// detectNode reads package.json and the lockfiles to work out the
// toolchain. It returns nil when the repository is not a Node project.
func detectNode(src Source) *Node {
	raw, err := src.Read("package.json")
	if err != nil {
		return nil
	}

	var pkg struct {
		Main            string            `json:"main"`
		Type            string            `json:"type"`
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Engines         struct {
			Node string `json:"node"`
		} `json:"engines"`
		PackageManager string `json:"packageManager"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return nil
	}

	n := &Node{
		Scripts:         pkg.Scripts,
		Dependencies:    pkg.Dependencies,
		DevDependencies: pkg.DevDependencies,
		Main:            pkg.Main,
		Type:            pkg.Type,
	}

	// The lockfile is the authority: it is what makes an install
	// reproducible, and installing it with the wrong manager produces a
	// different dependency tree than the developer tested against.
	switch {
	case src.Exists("pnpm-lock.yaml"):
		n.Manager, n.Lockfile = "pnpm", "pnpm-lock.yaml"
	case src.Exists("yarn.lock"):
		n.Manager, n.Lockfile = "yarn", "yarn.lock"
	case src.Exists("bun.lockb"):
		n.Manager, n.Lockfile = "bun", "bun.lockb"
	case src.Exists("bun.lock"):
		n.Manager, n.Lockfile = "bun", "bun.lock"
	case src.Exists("package-lock.json"):
		n.Manager, n.Lockfile = "npm", "package-lock.json"
	default:
		// No lockfile: fall back to what packageManager declares, then npm.
		n.Manager = "npm"
		if name, _, ok := strings.Cut(pkg.PackageManager, "@"); ok && name != "" {
			n.Manager = name
		}
	}

	switch n.Manager {
	case "pnpm":
		n.Install, n.Run = "pnpm install --frozen-lockfile", "pnpm run"
	case "yarn":
		n.Install, n.Run = "yarn install --frozen-lockfile", "yarn"
	case "bun":
		n.Install, n.Run = "bun install --frozen-lockfile", "bun run"
	default:
		n.Manager = "npm"
		n.Install, n.Run = "npm ci", "npm run"
	}
	// `npm ci` and the --frozen-lockfile variants all fail outright without
	// a lockfile, which would be a confusing first deploy for a repository
	// that simply has not committed one.
	if n.Lockfile == "" {
		switch n.Manager {
		case "pnpm":
			n.Install = "pnpm install --no-frozen-lockfile"
		case "yarn":
			n.Install = "yarn install"
		case "bun":
			n.Install = "bun install"
		default:
			n.Install = "npm install"
		}
	}

	n.Version = nodeVersion(src, pkg.Engines.Node)
	return n
}

var majorRe = regexp.MustCompile(`(\d+)`)

// nodeVersion resolves the major Node version to build on, preferring what
// the repository pinned.
func nodeVersion(src Source, engines string) string {
	for _, candidate := range []string{engines, readText(src, ".nvmrc"), readText(src, ".node-version")} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		// An engines range like ">=18 <21" should build on something the
		// range allows; its lowest bound always is.
		m := majorRe.FindString(candidate)
		if m == "" {
			continue
		}
		major, err := strconv.Atoi(m)
		// Node 18 is the oldest release still receiving security fixes at
		// the time of writing; below that, build on the default instead of
		// pulling an image that no longer gets patched.
		if err != nil || major < 18 || major > 99 {
			continue
		}
		return strconv.Itoa(major)
	}
	return DefaultNodeVersion
}

// BuilderImage is the image a Node project builds and runs in.
func (n *Node) BuilderImage() string {
	if n != nil && n.Manager == "bun" {
		// Bun is a runtime as well as a package manager, so its own image
		// is the right base rather than Node plus a curl install.
		return "oven/bun:1-alpine"
	}
	v := DefaultNodeVersion
	if n != nil && n.Version != "" {
		v = n.Version
	}
	return "node:" + v + "-alpine"
}

// setupManager renders the Dockerfile lines that make the chosen package
// manager available. corepack ships with Node and is the supported way to
// get the exact pnpm or yarn a project expects.
func (n *Node) setupManager() string {
	switch n.Manager {
	case "pnpm":
		return "RUN corepack enable && corepack prepare pnpm@latest --activate"
	case "yarn":
		return "RUN corepack enable"
	case "bun":
		// The official bun image already has it; nothing to install.
		return ""
	default:
		return ""
	}
}

// lockfileCopy renders the COPY that seeds the dependency layer. The
// wildcard keeps the build working when a lockfile is absent, which COPY
// of a named missing file would not.
func (n *Node) lockfileCopy() string {
	switch n.Manager {
	case "pnpm":
		return "COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml* .npmrc* ./"
	case "yarn":
		return "COPY package.json yarn.lock* .yarnrc* .yarnrc.yml* ./"
	case "bun":
		return "COPY package.json bun.lock* bun.lockb* ./"
	default:
		return "COPY package.json package-lock.json* npm-shrinkwrap.json* .npmrc* ./"
	}
}
