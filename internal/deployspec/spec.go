// Package deployspec defines deployment.yaml — the single file a repository
// carries to describe how it is built, run and routed.
//
// The design goal is that a correct file is short. Everything here has a
// defensible default, and the common cases (a repo with a Dockerfile, a repo
// with a compose file, a static site) need only a handful of lines.
package deployspec

import "time"

// Filenames are the names publix looks for at the repository root.
var Filenames = []string{"deployment.yaml", "deployment.yml", ".publix/deployment.yaml"}

// Kind selects how a project is built and run.
type Kind string

const (
	// KindAuto asks publix to detect the kind from the repository contents.
	KindAuto Kind = "auto"
	// KindDockerfile builds a single image and runs it directly.
	KindDockerfile Kind = "dockerfile"
	// KindCompose hands the whole stack to docker compose.
	KindCompose Kind = "compose"
	// KindFramework lets publix generate the Dockerfile from the
	// repository's own framework configuration. This is the default for a
	// Next.js, Nuxt, SvelteKit, Remix, Go or Python project that has not
	// written a Dockerfile — which is most of them.
	KindFramework Kind = "framework"
	// KindStatic runs a build command and serves the output directory.
	KindStatic Kind = "static"
	// KindImage deploys a prebuilt image without building anything.
	KindImage Kind = "image"
)

// Spec is the root of deployment.yaml.
type Spec struct {
	// Name is a human label for the project. When a project is imported
	// from GitHub the repository name is used if this is absent.
	Name string `yaml:"name,omitempty"`

	// Kind selects the build strategy. Defaults to auto-detection.
	Kind Kind `yaml:"type,omitempty"`

	// Dockerfile is the path to the Dockerfile, relative to Context.
	Dockerfile string `yaml:"dockerfile,omitempty"`

	// Compose is the path to the compose file. When Kind is auto and a
	// compose file exists at a conventional name, it is found without this.
	Compose string `yaml:"compose,omitempty"`

	// Framework pins which framework template to use, e.g. "nextjs". It is
	// detected from the repository's config files when omitted, so this is
	// only needed to override a wrong guess.
	Framework string `yaml:"framework,omitempty"`

	// Service names the compose service that receives the project's
	// domains. Required only when the compose file has more than one
	// service that publishes a port.
	Service string `yaml:"service,omitempty"`

	// Image is the prebuilt image reference for type: image.
	Image string `yaml:"image,omitempty"`

	// Context is the build context directory, relative to the repo root.
	Context string `yaml:"context,omitempty"`

	// Port is the port the application listens on inside its container.
	Port int `yaml:"port,omitempty"`

	// Replicas is how many containers serve the project. Ignored for
	// compose projects, where the compose file decides.
	//
	// It is a pointer so that an explicitly written `replicas: 0` is a
	// validation error rather than being silently defaulted to 1.
	Replicas *int `yaml:"replicas,omitempty"`

	// Command overrides the image's default command.
	Command []string `yaml:"command,omitempty"`

	// Env are runtime environment variables. Values may reference
	// ${secret.KEY} for values held by the server and ${publix.KEY} for
	// deployment metadata such as the commit sha.
	Env map[string]string `yaml:"env,omitempty"`

	// Domains are the hostnames this project answers on in production.
	Domains []string `yaml:"domains,omitempty"`

	// Routes are domains needing more than a hostname: a path prefix,
	// a redirect, or basic auth.
	Routes []Route `yaml:"routes,omitempty"`

	// Volumes attach server-registered shared volumes. A bare name mounts
	// at /shared/<name>.
	Volumes []Volume `yaml:"volumes,omitempty"`

	Build     Build     `yaml:"build,omitempty"`
	Health    Health    `yaml:"health,omitempty"`
	Resources Resources `yaml:"resources,omitempty"`
	Release   Release   `yaml:"release,omitempty"`

	// Cron are scheduled one-shot runs of the project image.
	Cron []Cron `yaml:"cron,omitempty"`
}

// Route is one hostname with non-default handling.
type Route struct {
	Domain     string            `yaml:"domain"`
	Path       string            `yaml:"path,omitempty"`
	StripPath  bool              `yaml:"stripPath,omitempty"`
	RedirectTo string            `yaml:"redirectTo,omitempty"`
	TLS        *bool             `yaml:"tls,omitempty"`
	Headers    map[string]string `yaml:"headers,omitempty"`
	BasicAuth  []string          `yaml:"basicAuth,omitempty"`
	// Service overrides which compose service this route reaches.
	Service string `yaml:"service,omitempty"`
}

// Volume attaches a shared volume registered on the server.
//
// The server owns the host path; the project only chooses which volume it
// wants and where it lands inside the container. Each project gets its own
// subdirectory named for its ID, so projects can never read each other's
// data through a shared volume.
type Volume struct {
	// Name is the server-registered volume name, e.g. "disk0".
	Name string `yaml:"name"`
	// MountPath overrides the default /shared/<name>.
	MountPath string `yaml:"mountPath,omitempty"`
	// SubPath mounts only a subdirectory of the project's area.
	SubPath string `yaml:"subPath,omitempty"`
	// ReadOnly mounts without write access.
	ReadOnly bool `yaml:"readOnly,omitempty"`
	// Services limits the mount to named compose services. Empty means
	// every service in the stack.
	Services []string `yaml:"services,omitempty"`
}

// UnmarshalYAML accepts either a bare volume name or a full mapping, so the
// common case is one word.
func (v *Volume) UnmarshalYAML(unmarshal func(any) error) error {
	var name string
	if err := unmarshal(&name); err == nil {
		v.Name = name
		return nil
	}
	type raw Volume
	var r raw
	if err := unmarshal(&r); err != nil {
		return err
	}
	*v = Volume(r)
	return nil
}

// Mount returns the in-container path for this volume.
func (v Volume) Mount() string {
	if v.MountPath != "" {
		return v.MountPath
	}
	return "/shared/" + v.Name
}

// Build holds settings specific to producing an image.
type Build struct {
	// Args are --build-arg values for a Dockerfile build, and extra
	// environment variables for a static build.
	Args map[string]string `yaml:"args,omitempty"`
	// Target selects a stage of a multi-stage Dockerfile.
	Target string `yaml:"target,omitempty"`
	// Pull refreshes base images on every build.
	Pull bool `yaml:"pull,omitempty"`

	// Command is the build command, e.g. "npm run build". Detected from
	// the repository when omitted.
	Command string `yaml:"command,omitempty"`
	// Install runs before Command, e.g. "npm ci". Detected from the
	// repository's lockfile when omitted.
	Install string `yaml:"install,omitempty"`
	// Start is the command the container runs. Detected when omitted;
	// setting it also forces the project to be treated as a server.
	Start string `yaml:"start,omitempty"`
	// Output is the directory a static build emits.
	Output string `yaml:"output,omitempty"`
	// SPA rewrites unknown paths to index.html.
	SPA *bool `yaml:"spa,omitempty"`
	// Builder is the image the build runs in, e.g. "node:20-alpine".
	// Override it to pin a toolchain version or add system packages.
	Builder string `yaml:"builder,omitempty"`
	// Runtime is the image the application runs in.
	Runtime string `yaml:"runtime,omitempty"`
}

// HealthType selects how readiness is probed.
type HealthType string

// Health probe kinds.
const (
	HealthHTTP HealthType = "http"
	HealthTCP  HealthType = "tcp"
	HealthExec HealthType = "exec"
	HealthNone HealthType = "none"
)

// Health is the readiness gate a new deployment must pass before it is
// allowed to receive traffic. It is the difference between a bad deploy
// being a rolled-back non-event and being an outage.
type Health struct {
	Type     HealthType        `yaml:"type,omitempty"`
	Path     string            `yaml:"path,omitempty"`
	Port     int               `yaml:"port,omitempty"`
	Status   int               `yaml:"status,omitempty"`
	Command  []string          `yaml:"command,omitempty"`
	Interval Duration          `yaml:"interval,omitempty"`
	Timeout  Duration          `yaml:"timeout,omitempty"`
	Grace    Duration          `yaml:"grace,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty"`
}

// Resources caps what the project may consume, per replica.
type Resources struct {
	CPU               string `yaml:"cpu,omitempty"`
	Memory            string `yaml:"memory,omitempty"`
	MemoryReservation string `yaml:"memoryReservation,omitempty"`
	PidsLimit         int64  `yaml:"pidsLimit,omitempty"`
}

// Strategy selects how a new deployment replaces the live one.
type Strategy string

const (
	// StrategyBlueGreen starts the new containers alongside the old, waits
	// for health, moves traffic, then reaps the old set. Zero downtime,
	// with both sets resident for a few seconds.
	StrategyBlueGreen Strategy = "blue-green"
	// StrategyRecreate stops the old containers first. Brief downtime, but
	// never more than one set resident — the right choice on a small host
	// or for a project holding an exclusive resource such as a port bind
	// or a single-writer volume.
	StrategyRecreate Strategy = "recreate"
)

// Release controls the cutover and what is retained afterwards.
type Release struct {
	Strategy Strategy `yaml:"strategy,omitempty"`
	// Drain is how long the outgoing containers keep serving in-flight
	// requests after traffic has moved.
	Drain Duration `yaml:"drain,omitempty"`
	// AutoRollback restores the previous deployment when the new one
	// fails its health gate.
	AutoRollback *bool `yaml:"autoRollback,omitempty"`
	// Branch is the git branch that deploys to production.
	Branch string `yaml:"branch,omitempty"`
}

// Cron is a scheduled one-shot run of the project image.
type Cron struct {
	Name     string   `yaml:"name"`
	Schedule string   `yaml:"schedule"`
	Command  []string `yaml:"command"`
	Timeout  Duration `yaml:"timeout,omitempty"`
	// Service selects which compose service's image to run.
	Service string `yaml:"service,omitempty"`
}

// Duration is a time.Duration that reads "30s", "5m", "1h" from YAML.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a duration string, or a bare number as seconds.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var n float64
	if err := unmarshal(&n); err != nil {
		return err
	}
	*d = Duration(time.Duration(n * float64(time.Second)))
	return nil
}

// MarshalYAML renders the duration back as a human string.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// ReplicaCount returns the effective replica count.
func (s *Spec) ReplicaCount() int {
	if s.Replicas == nil {
		return 1
	}
	return *s.Replicas
}
