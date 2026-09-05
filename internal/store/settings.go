// Package store holds everything the platform knows: server settings,
// registered shared volumes, projects, their secrets, and their deployment
// history. It is a single JSON document written atomically, which is the
// right amount of machinery for a self-hosted control plane and leaves
// nothing for an operator to administer.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Settings is the server-wide configuration, owned by the operator rather
// than by any repository.
type Settings struct {
	// Network is the docker network shared by Traefik and every project.
	Network string `json:"network"`

	// TraefikDynamicDir is the directory Traefik's file provider watches.
	// publix owns exactly one file inside it.
	TraefikDynamicDir string `json:"traefikDynamicDir"`

	// EntryPoints are the Traefik entrypoints project routes attach to.
	EntryPoints []string `json:"entryPoints"`

	// CertResolver is the Traefik ACME resolver name. Empty disables TLS.
	CertResolver string `json:"certResolver"`

	// AppsDomain is the wildcard parent domain used to give every project a
	// working URL before anyone configures a custom domain, e.g.
	// "apps.example.com" yields "<project>.apps.example.com".
	AppsDomain string `json:"appsDomain,omitempty"`

	// Volumes are host directories the operator has made available to
	// projects. See Volume for the two scopes and what each guarantees.
	Volumes []Volume `json:"volumes,omitempty"`

	// LegacySharedVolumes is where volumes lived before they had a scope.
	// It is migrated into Volumes on load and then left empty; the field
	// remains only so an older state file still opens.
	LegacySharedVolumes []Volume `json:"sharedVolumes,omitempty"`

	// WorkDir is where publix keeps repository checkouts.
	WorkDir string `json:"workDir"`

	// KeepImages is how many images per project are retained. Two is the
	// floor that still allows an instant rollback: the live one and the one
	// before it. Older deployments roll back by rebuilding from their commit.
	KeepImages int `json:"keepImages"`

	// KeepDeployments is how many deployment records are retained per
	// project. These are metadata only and cost almost nothing, but they
	// are what the rollback list is built from.
	KeepDeployments int `json:"keepDeployments"`

	// BuildConcurrency caps simultaneous builds across all projects.
	BuildConcurrency int `json:"buildConcurrency"`

	// LogDriver and LogOptions configure container logging.
	LogDriver  string            `json:"logDriver,omitempty"`
	LogOptions map[string]string `json:"logOptions,omitempty"`

	// GitHub holds the platform's GitHub credentials.
	GitHub GitHubSettings `json:"github,omitempty"`

	// PublicURL is the externally reachable address of this dashboard. It
	// is what GitHub webhooks and OAuth callbacks are pointed at.
	PublicURL string `json:"publicUrl,omitempty"`

	// Auth holds the dashboard login credentials.
	Auth AuthSettings `json:"auth,omitempty"`
}

// VolumeScope decides what a project actually gets when it mounts a volume.
type VolumeScope string

const (
	// ScopeProject gives every project its own directory inside the
	// volume, named after the project ID. Two projects can mount the same
	// volume and neither can see the other's files. This is the default,
	// and the right answer for a project's own uploads or cache.
	ScopeProject VolumeScope = "project"

	// ScopeShared mounts the volume's directory itself into every project
	// that asks for it. They read and write the same files.
	//
	// There is no isolation here, deliberately: it is what makes a shared
	// dataset, a media library or a common cache possible. It also means
	// one project can destroy another's data, so it is never the default.
	ScopeShared VolumeScope = "shared"
)

// Volume is a host directory the operator exposes to projects.
//
// Projects never name a host path. They ask for a volume by name, and the
// server decides which directory that resolves to — which is what keeps a
// repository from reaching anywhere the operator did not offer.
type Volume struct {
	// Name is what projects reference in deployment.yaml, e.g. "disk0".
	Name string `json:"name"`
	// Path is the host directory this volume is rooted at.
	Path string `json:"path"`
	// Scope decides whether projects get their own directory inside Path
	// or all share Path itself.
	Scope VolumeScope `json:"scope,omitempty"`
	// Description is shown in the dashboard.
	Description string `json:"description,omitempty"`
	// ReadOnly forces every mount of this volume to be read-only.
	ReadOnly bool `json:"readOnly,omitempty"`
	// DefaultMount overrides the /shared/<name> convention.
	DefaultMount string `json:"defaultMount,omitempty"`
}

// Shared reports whether every project mounts the same directory.
func (v Volume) Shared() bool { return v.Scope == ScopeShared }

// Mount returns the in-container path this volume mounts at by default.
func (v Volume) Mount() string {
	if v.DefaultMount != "" {
		return v.DefaultMount
	}
	return "/shared/" + v.Name
}

// Dir returns the host directory a project mounts from this volume.
//
// This is the only place the two scopes differ, and it is the whole of the
// isolation model: a project-scoped volume resolves to a directory named
// after the project, a shared one to the volume's own root.
func (v Volume) Dir(projectID string) string {
	if v.Shared() {
		return v.Path
	}
	return filepath.Join(v.Path, projectID)
}

// GitHubSettings holds the platform's GitHub credentials. Either a personal
// access token (fastest to set up) or a GitHub App (right for organisations,
// and the only option that can create webhooks on repos you do not own).
type GitHubSettings struct {
	// Token is a personal access token with `repo` scope.
	Token string `json:"token,omitempty"`
	// AppID, InstallationID and PrivateKey configure a GitHub App.
	AppID          string `json:"appId,omitempty"`
	InstallationID string `json:"installationId,omitempty"`
	PrivateKey     string `json:"privateKey,omitempty"`
	// WebhookSecret validates incoming webhook payloads.
	WebhookSecret string `json:"webhookSecret,omitempty"`
	// APIBase supports GitHub Enterprise.
	APIBase string `json:"apiBase,omitempty"`
	// Login is the authenticated account, cached for display.
	Login string `json:"login,omitempty"`
}

// Configured reports whether GitHub can be talked to at all.
func (g GitHubSettings) Configured() bool {
	return g.Token != "" || (g.AppID != "" && g.PrivateKey != "")
}

// AuthSettings holds the dashboard credentials.
type AuthSettings struct {
	// PasswordHash is a PBKDF2-SHA256 hash of the admin password.
	PasswordHash string `json:"passwordHash,omitempty"`
	// Salt is the per-install password salt.
	Salt string `json:"salt,omitempty"`
	// SessionKey signs session cookies.
	SessionKey string `json:"sessionKey,omitempty"`
}

// DefaultSettings returns the configuration publix starts with.
func DefaultSettings() Settings {
	return Settings{
		Network:           "publix",
		TraefikDynamicDir: "/etc/traefik/dynamic",
		EntryPoints:       []string{"websecure"},
		CertResolver:      "letsencrypt",
		WorkDir:           filepath.Join(Home(), "work"),
		KeepImages:        2,
		KeepDeployments:   30,
		BuildConcurrency:  2,
		LogDriver:         "json-file",
		LogOptions:        map[string]string{"max-size": "10m", "max-file": "3"},
	}
}

// TLSEnabled reports whether routes get certificates.
func (s *Settings) TLSEnabled() bool { return s.CertResolver != "" }

// Volume looks up a registered volume by name.
func (s *Settings) Volume(name string) (Volume, bool) {
	for _, v := range s.Volumes {
		if v.Name == name {
			return v, true
		}
	}
	return Volume{}, false
}

// VolumeNames lists every registered volume, for an error that needs to
// say what is actually available.
func (s *Settings) VolumeNames() []string {
	out := make([]string, 0, len(s.Volumes))
	for _, v := range s.Volumes {
		out = append(out, v.Name)
	}
	return out
}

var volumeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidateVolume checks a volume registration before it is saved. Getting
// this wrong exposes host paths to every project on the box, so the checks
// are deliberately strict.
func (s *Settings) ValidateVolume(v Volume, editing string) error {
	var errs []string
	if !volumeNameRe.MatchString(v.Name) {
		errs = append(errs, fmt.Sprintf("name %q must be lowercase alphanumeric with dots, dashes or underscores", v.Name))
	}
	if !filepath.IsAbs(v.Path) {
		errs = append(errs, fmt.Sprintf("path %q must be absolute", v.Path))
	}
	if clean := filepath.Clean(v.Path); clean != filepath.Clean(v.Path) || strings.Contains(v.Path, "..") {
		errs = append(errs, "path must not contain \"..\"")
	}
	// Handing out a subdirectory of any of these would let a project read
	// or overwrite the host's own state.
	for _, forbidden := range []string{"/", "/etc", "/usr", "/bin", "/sbin", "/lib", "/boot", "/dev", "/proc", "/sys", "/var/run", "/root"} {
		if filepath.Clean(v.Path) == forbidden {
			errs = append(errs, fmt.Sprintf("path %q is a system directory and cannot be shared with projects", v.Path))
		}
	}
	if v.DefaultMount != "" && !strings.HasPrefix(v.DefaultMount, "/") {
		errs = append(errs, fmt.Sprintf("defaultMount %q must be an absolute path", v.DefaultMount))
	}
	switch v.Scope {
	case ScopeProject, ScopeShared:
	default:
		errs = append(errs, fmt.Sprintf("scope %q is not one of project, shared", v.Scope))
	}
	for _, existing := range s.Volumes {
		if existing.Name == v.Name && existing.Name != editing {
			errs = append(errs, fmt.Sprintf("a volume named %q already exists", v.Name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid volume:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// EnsureDir creates the directory a project mounts from this volume.
func (v Volume) EnsureDir(projectID string) (string, error) {
	dir := v.Dir(projectID)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", fmt.Errorf("creating %s for volume %q: %w", dir, v.Name, err)
	}
	// The container's user is unknown and frequently non-root, so the
	// directory has to be writable by whoever the image runs as.
	//
	// For a project-scoped volume that is contained: the directory belongs
	// to one project. For a shared one it is the point — every project
	// that mounts it writes to the same place.
	if err := os.Chmod(dir, 0o777); err != nil {
		return "", err
	}
	return dir, nil
}

// Home is the directory publix keeps its state in.
func Home() string {
	if h := os.Getenv("PUBLIX_HOME"); h != "" {
		return h
	}
	if os.Geteuid() == 0 {
		return "/var/lib/publix"
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".publix")
	}
	return ".publix"
}
