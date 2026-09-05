package deployspec

import (
	"github.com/Oein/publix/internal/compose"
	"github.com/Oein/publix/internal/framework"
)

// Detection is what publix inferred about a repository. It is what the
// import screen shows before anything has been cloned, and what fills in
// everything deployment.yaml left unsaid at deploy time.
type Detection struct {
	Kind Kind `json:"kind"`
	// Framework is the stable identifier, e.g. "nextjs".
	Framework string `json:"framework,omitempty"`
	// Name is the human label, e.g. "Next.js".
	Name string `json:"name,omitempty"`
	// ConfigFile is the file that identified it, so the dashboard can say
	// why it concluded what it did.
	ConfigFile string `json:"configFile,omitempty"`

	Dockerfile string `json:"dockerfile,omitempty"`
	Compose    string `json:"compose,omitempty"`
	Service    string `json:"service,omitempty"`

	Port    int    `json:"port,omitempty"`
	Install string `json:"install,omitempty"`
	Command string `json:"command,omitempty"`
	Start   string `json:"start,omitempty"`
	Output  string `json:"output,omitempty"`
	SPA     bool   `json:"spa,omitempty"`

	// Generated reports whether publix will write the Dockerfile itself,
	// which is the difference between "this just works" and "add a
	// Dockerfile first".
	Generated bool `json:"generated"`
	// Standalone marks a Next.js project using standalone output.
	Standalone bool `json:"standalone,omitempty"`

	Notes []string `json:"notes,omitempty"`

	// result carries the full framework detection through to the builder.
	result *framework.Result
}

// Result returns the underlying framework detection.
func (d Detection) Result() *framework.Result { return d.result }

// Detect inspects a checkout and infers how it should be deployed.
func Detect(root string) Detection { return DetectFrom(framework.Dir(root)) }

// DetectFrom inspects any repository source — a local checkout, or a
// repository on GitHub that has not been cloned. Both paths run this same
// code, so the import screen cannot disagree with the deploy.
func DetectFrom(src framework.Source) Detection {
	r := framework.Detect(src)

	d := Detection{
		Framework:  r.ID,
		Name:       r.Name,
		ConfigFile: r.ConfigFile,
		Dockerfile: r.Dockerfile,
		Compose:    r.Compose,
		Port:       r.Port,
		Install:    r.Install,
		Command:    r.Build,
		Start:      r.Start,
		Output:     r.Output,
		SPA:        r.SPA,
		Standalone: r.Standalone,
		Generated:  r.Generated(),
		Notes:      r.Notes,
		result:     r,
	}

	switch {
	case r.Compose != "":
		d.Kind = KindCompose
		if svc, port := composeGuess(src, r.Compose); svc != "" {
			d.Service, d.Port = svc, port
		}
	case r.Dockerfile != "":
		d.Kind = KindDockerfile
	case r.Mode == framework.ModeStatic:
		d.Kind = KindStatic
	case r.Mode == framework.ModeServer:
		// A framework publix can containerise by itself. This is the case
		// that used to be a dead end: "we know it is Next.js, now go write
		// a Dockerfile."
		d.Kind = KindFramework
	default:
		d.Kind = KindDockerfile
	}
	return d
}

// composeGuess reads a compose file through the source abstraction so the
// GitHub path can use it without a checkout.
func composeGuess(src framework.Source, path string) (string, int) {
	raw, err := src.Read(path)
	if err != nil {
		return "", 0
	}
	f, err := compose.ParseBytes(raw, path)
	if err != nil {
		return "", 0
	}
	return f.Guess()
}
