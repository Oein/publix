// Package store holds everything the platform knows: server settings,
// registered shared volumes, projects, their secrets, and their deployment
// history. It is a single JSON document written atomically, which is the
// right amount of machinery for a self-hosted control plane and leaves
// nothing for an operator to administer.
package store

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	// AppsDomains are the wildcard parent domains registered by the
	// operator. Each gives projects a working URL before anyone configures
	// a custom domain: "apps.example.com" yields "<project>.apps.example.com".
	//
	// There is a list rather than one because a server usually hosts more
	// than one thing — staging and production, or two clients — and which
	// parent a project sits under is a per-project decision made when it is
	// imported.
	AppsDomains []AppsDomain `json:"appsDomains,omitempty"`

	// LegacyAppsDomain is where the single apps domain lived before there
	// could be several. It is migrated into AppsDomains on load and then
	// left empty; the field remains only so an older state file opens.
	LegacyAppsDomain string `json:"appsDomain,omitempty"`

	// Proxies attach a hostname to a backend publix does not deploy: an
	// app on another host, a container someone else runs, a device on the
	// LAN. Traefik proxies to it, so the address in the browser stays.
	Proxies []Proxy `json:"proxies,omitempty"`

	// Redirects forward hostnames that have no project behind them —
	// a retired domain, a www that should reach the apex, a vanity name
	// pointing at somewhere else entirely.
	Redirects []Redirect `json:"redirects,omitempty"`

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

// AppsDomain is a wildcard parent domain registered by the operator.
//
// Registering one is a claim about DNS, not about any project: it says
// *.<domain> resolves to this host. Projects then pick which registered
// parent their generated hostname sits under.
type AppsDomain struct {
	// Domain is the parent, e.g. "apps.example.com".
	Domain string `json:"domain"`
	// Default marks the one new projects get when they express no
	// preference. Exactly one registered domain is the default.
	Default bool `json:"default,omitempty"`
	// Description is shown in the dashboard, to tell two similar domains
	// apart at a glance.
	Description string `json:"description,omitempty"`
}

// AppsDomainNone is the value a project uses to opt out of a generated
// hostname entirely, when it should answer only on its own domains.
//
// It cannot collide with a real registration: every apps domain must
// contain a dot, and this does not.
const AppsDomainNone = "none"

// Proxy routes a hostname to a backend publix does not manage.
//
// This is the opposite of a Redirect: the request is proxied, so the
// visitor's address bar keeps the hostname they typed and the backend can
// be anything reachable from the Traefik container — another machine, a
// container from a different compose project, a box on the LAN.
//
// It exists because a server that terminates TLS for a domain is the
// natural place to put everything on that domain, deployed here or not.
type Proxy struct {
	// Domain is the hostname visitors arrive on.
	Domain string `json:"domain"`
	// Path narrows the rule to a prefix. Empty proxies the whole host.
	Path string `json:"path,omitempty"`
	// Target is the backend, as an absolute URL: http://10.0.0.5:3000.
	Target string `json:"target"`
	// StripPath removes Path before proxying, for a backend mounted at its
	// own root rather than under the prefix.
	StripPath bool `json:"stripPath,omitempty"`
	// PassHostHeader forwards the visitor's Host to the backend. It
	// defaults to true, which is what a self-hosted app expects; turn it
	// off for a backend that routes on its own hostname and would
	// otherwise not recognise the request.
	PassHostHeader *bool `json:"passHostHeader,omitempty"`
	// InsecureSkipVerify accepts a backend HTTPS certificate that does not
	// validate. Only for a backend on your own network with a self-signed
	// certificate.
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// Description is shown in the dashboard.
	Description string `json:"description,omitempty"`
}

// PassesHost reports whether the visitor's Host reaches the backend.
func (p Proxy) PassesHost() bool { return p.PassHostHeader == nil || *p.PassHostHeader }

// TargetURL is the backend as an absolute URL. A bare host:port becomes
// http, since a backend given without a scheme is almost never TLS.
func (p Proxy) TargetURL() string {
	t := strings.TrimSuffix(strings.TrimSpace(p.Target), "/")
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		return t
	}
	return "http://" + t
}

// Redirect forwards one hostname somewhere else, with nothing deployed
// behind it.
//
// A project can already declare redirects in its deployment.yaml, but those
// belong to the project and disappear with it. These belong to the server,
// which is where a domain you no longer host anything on has to live.
type Redirect struct {
	// Domain is the hostname being forwarded.
	Domain string `json:"domain"`
	// Path narrows the rule to a prefix. Empty forwards the whole host.
	Path string `json:"path,omitempty"`
	// Target is where requests go: a hostname, or a full URL when the
	// scheme matters or the destination is a fixed page.
	Target string `json:"target"`
	// KeepPath appends the request's path and query to the target. It
	// defaults to true, because a domain move should not turn every
	// bookmark under the old host into a landing on the new home page.
	KeepPath *bool `json:"keepPath,omitempty"`
	// Permanent sends 301 rather than 302.
	//
	// It defaults to off: a browser caches a permanent redirect more or
	// less forever, so getting one wrong is expensive to undo and the
	// person adding it usually cannot tell yet whether it is right.
	Permanent bool `json:"permanent,omitempty"`
	// Description is shown in the dashboard.
	Description string `json:"description,omitempty"`
}

// Keeps reports whether the request path is carried across.
func (r Redirect) Keeps() bool { return r.KeepPath == nil || *r.KeepPath }

// TargetURL is the destination as an absolute URL. A bare hostname becomes
// https, since that is the only thing worth redirecting to.
func (r Redirect) TargetURL() string {
	t := strings.TrimSuffix(strings.TrimSpace(r.Target), "/")
	if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
		return t
	}
	return "https://" + t
}

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

// DefaultAppsDomain is the parent a project gets when it expresses no
// preference. With none marked default the first registered one is used,
// so a list can never be non-empty and yet yield nothing.
func (s *Settings) DefaultAppsDomain() string {
	for _, d := range s.AppsDomains {
		if d.Default {
			return d.Domain
		}
	}
	if len(s.AppsDomains) > 0 {
		return s.AppsDomains[0].Domain
	}
	return ""
}

// HasAppsDomain reports whether a domain is registered.
func (s *Settings) HasAppsDomain(domain string) bool {
	for _, d := range s.AppsDomains {
		if strings.EqualFold(d.Domain, domain) {
			return true
		}
	}
	return false
}

// AppsDomainNames lists every registered parent, for an error that has to
// say what is actually on offer.
func (s *Settings) AppsDomainNames() []string {
	out := make([]string, 0, len(s.AppsDomains))
	for _, d := range s.AppsDomains {
		out = append(out, d.Domain)
	}
	return out
}

// AppsDomainFor resolves the parent domain a project's generated hostname
// sits under.
//
// A choice that is no longer registered falls back to the default rather
// than leaving the project unreachable: the operator removed a domain, and
// silently dropping every project that used it is the worse failure. Only
// an explicit opt-out yields nothing.
func (s *Settings) AppsDomainFor(p *Project) string {
	if p == nil {
		return s.DefaultAppsDomain()
	}
	switch {
	case p.AppsDomain == AppsDomainNone:
		return ""
	case p.AppsDomain != "" && s.HasAppsDomain(p.AppsDomain):
		return p.AppsDomain
	default:
		return s.DefaultAppsDomain()
	}
}

// Proxy looks up a registered proxy rule.
func (s *Settings) Proxy(domain, path string) (Proxy, bool) {
	for _, p := range s.Proxies {
		if strings.EqualFold(p.Domain, domain) && p.Path == path {
			return p, true
		}
	}
	return Proxy{}, false
}

// ValidateProxy checks a proxy rule before it is saved.
func (s *Settings) ValidateProxy(p Proxy, replacing string) error {
	if p.Domain == "" {
		return fmt.Errorf("a hostname is required")
	}
	if !hostnameRe.MatchString(p.Domain) {
		return fmt.Errorf("%q is not a hostname", p.Domain)
	}
	if strings.TrimSpace(p.Target) == "" {
		return fmt.Errorf("a target is required — where should %s go?", p.Domain)
	}

	u, err := url.Parse(p.TargetURL())
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a URL publix can proxy to — it should look like http://10.0.0.5:3000", p.Target)
	}
	// Proxying a hostname to itself is a loop Traefik will happily serve
	// until something times out.
	if strings.EqualFold(u.Hostname(), p.Domain) {
		return fmt.Errorf("%s would proxy to itself", p.Domain)
	}
	if p.Path != "" && !strings.HasPrefix(p.Path, "/") {
		return fmt.Errorf("a path must start with /, got %q", p.Path)
	}
	if p.StripPath && p.Path == "" {
		return fmt.Errorf("there is no path to strip — set a path prefix, or leave stripping off")
	}

	for _, existing := range s.Proxies {
		if strings.EqualFold(existing.Domain, replacing) && existing.Path == p.Path {
			continue
		}
		if strings.EqualFold(existing.Domain, p.Domain) && existing.Path == p.Path {
			return fmt.Errorf("%s is already proxied", p.Domain)
		}
	}
	// One hostname cannot both proxy and redirect: whichever Traefik picked
	// would be arbitrary.
	for _, r := range s.Redirects {
		if strings.EqualFold(r.Domain, p.Domain) && r.Path == p.Path {
			return fmt.Errorf("%s is already redirected to %s; remove that rule first", p.Domain, r.TargetURL())
		}
	}
	return nil
}

// Redirect looks up a registered forwarding rule.
func (s *Settings) Redirect(domain, path string) (Redirect, bool) {
	for _, r := range s.Redirects {
		if strings.EqualFold(r.Domain, domain) && r.Path == path {
			return r, true
		}
	}
	return Redirect{}, false
}

// ValidateRedirect checks a forwarding rule before it is saved.
func (s *Settings) ValidateRedirect(r Redirect, replacing string) error {
	if r.Domain == "" {
		return fmt.Errorf("a hostname to forward is required")
	}
	if !hostnameRe.MatchString(r.Domain) {
		return fmt.Errorf("%q is not a hostname", r.Domain)
	}
	if strings.TrimSpace(r.Target) == "" {
		return fmt.Errorf("a target is required — where should %s go?", r.Domain)
	}
	target := r.TargetURL()
	if host := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"); host == "" {
		return fmt.Errorf("%q is not a target", r.Target)
	}
	// A rule pointing at its own source is a loop the browser gives up on
	// after a dozen hops, with no clue as to why.
	if strings.EqualFold(strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"), r.Domain) && r.Path == "" {
		return fmt.Errorf("%s would forward to itself", r.Domain)
	}
	if r.Path != "" && !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("a path must start with /, got %q", r.Path)
	}
	for _, existing := range s.Redirects {
		if strings.EqualFold(existing.Domain, replacing) && existing.Path == r.Path {
			continue
		}
		if strings.EqualFold(existing.Domain, r.Domain) && existing.Path == r.Path {
			return fmt.Errorf("%s is already redirected", r.Domain)
		}
	}
	for _, p := range s.Proxies {
		if strings.EqualFold(p.Domain, r.Domain) && p.Path == r.Path {
			return fmt.Errorf("%s is already proxied to %s; remove that rule first", r.Domain, p.TargetURL())
		}
	}
	return nil
}

// hostnameRe is the same shape as an apps domain, but a redirect source may
// also be a wildcard-free single label under a suffix, so it is reused.
var hostnameRe = appsDomainRe

// appsDomainRe matches a hostname that can act as a wildcard parent. A
// leading "*." is accepted and stripped, because that is how the DNS record
// is written and pasting it in is the obvious mistake to forgive.
var appsDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// NormaliseAppsDomain cleans and validates a domain for registration.
func NormaliseAppsDomain(domain string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimSuffix(strings.TrimPrefix(d, "https://"), "/")
	d = strings.TrimSuffix(strings.TrimPrefix(d, "http://"), "/")
	d = strings.Trim(d, ".")
	if d == "" {
		return "", fmt.Errorf("a domain is required")
	}
	if d == AppsDomainNone {
		return "", fmt.Errorf("%q is reserved: it is how a project says it wants no generated hostname", AppsDomainNone)
	}
	if !appsDomainRe.MatchString(d) {
		return "", fmt.Errorf("%q is not a hostname — it should look like apps.example.com", domain)
	}
	return d, nil
}

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

// systemPaths are host directories a volume may never be rooted at or
// inside. Handing a project one of these is not a misconfiguration to warn
// about; it is a way to take over the machine.
//
// "/run" is here as well as "/var/run" because on a modern system one is a
// symlink to the other, and the Docker socket underneath either is root.
var systemPaths = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32",
	"/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib/docker",
	"/var/run",
}

// Deliberately absent: /mnt, /opt, /srv and /home. Those are where an
// operator actually keeps data, and refusing them would leave nowhere to
// put a volume.

// ProtectedPaths lists every directory this server refuses to hand out,
// including the ones it derives from its own configuration.
//
// publix's own state is the sharpest edge of all: the state file holds
// every project's secrets and the GitHub credentials, and the Traefik file
// decides what each hostname reaches. A project that could write either
// would own the platform, so those are refused alongside the system's.
func (s *Settings) ProtectedPaths() []string {
	paths := append([]string(nil), systemPaths...)
	for _, own := range []string{Home(), s.WorkDir, s.TraefikDynamicDir} {
		if own != "" && filepath.IsAbs(own) {
			paths = append(paths, filepath.Clean(own))
		}
	}
	sort.Strings(paths)
	return paths
}

// within reports whether path is dir or sits inside it.
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// resolve returns the path a volume really points at, following symlinks
// where it can. A link into /etc is still /etc, and checking only the name
// someone typed would miss it.
//
// A path that does not exist yet cannot be resolved, and is returned
// unchanged: it is checked as written, and the caller creates it.
func resolve(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(real)
	}
	return path
}

// ValidateVolume checks a volume registration before it is saved. Getting
// this wrong exposes host paths to every project on the box, so the checks
// are deliberately strict.
func (s *Settings) ValidateVolume(v Volume, editing string) error {
	var errs []string
	if !volumeNameRe.MatchString(v.Name) {
		errs = append(errs, fmt.Sprintf("name %q must be lowercase alphanumeric with dots, dashes or underscores", v.Name))
	}

	// Clean here rather than trusting the caller: ValidateVolume is the
	// gate, so it has to see the path the kernel will.
	path := filepath.Clean(v.Path)
	switch {
	case v.Path == "":
		errs = append(errs, "a host path is required")
	case !filepath.IsAbs(path):
		errs = append(errs, fmt.Sprintf("path %q must be absolute", v.Path))
	case path == "/":
		errs = append(errs, "path \"/\" is the whole host filesystem and cannot be shared with projects")
	default:
		real := resolve(path)
		for _, forbidden := range s.ProtectedPaths() {
			if within(path, forbidden) || within(real, forbidden) {
				where := path
				if real != path {
					where = fmt.Sprintf("%s (which resolves to %s)", path, real)
				}
				// "X is inside X" reads as a mistake when the path is the
				// protected directory itself.
				relation := "is inside"
				if path == forbidden || real == forbidden {
					relation = "is"
				}
				errs = append(errs, fmt.Sprintf(
					"path %s %s %s, which publix will not share with projects", where, relation, forbidden))
				break
			}
		}
	}

	// Nesting one volume inside another quietly destroys the isolation the
	// scopes promise: a shared volume containing a project volume's root
	// lets every project read every other project's directory.
	if filepath.IsAbs(path) {
		for _, existing := range s.Volumes {
			if existing.Name == editing {
				continue
			}
			other := filepath.Clean(existing.Path)
			if within(path, other) || within(other, path) {
				errs = append(errs, fmt.Sprintf(
					"path %s overlaps the volume %q at %s; one volume inside another defeats the isolation between them",
					path, existing.Name, other))
			}
		}
	}

	if v.DefaultMount != "" {
		switch {
		case !strings.HasPrefix(v.DefaultMount, "/"):
			errs = append(errs, fmt.Sprintf("defaultMount %q must be an absolute path", v.DefaultMount))
		case filepath.Clean(v.DefaultMount) == "/":
			errs = append(errs, "defaultMount \"/\" would mount over the container's whole filesystem")
		}
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
