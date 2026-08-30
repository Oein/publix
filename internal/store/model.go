package store

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Project is one deployable application on the platform.
type Project struct {
	// ID is a short, stable, opaque identifier. It never changes, and it
	// is what shared-volume directories are named after, so renaming a
	// project can never orphan or expose its data.
	ID string `json:"id"`

	// Slug is the URL-and-container-safe form of Name. It is derived from
	// Name but stored, because changing it moves the project's generated
	// hostname and that has to be a deliberate act.
	Slug string `json:"slug"`

	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Repo is the GitHub repository this project deploys from.
	Repo *Repo `json:"repo,omitempty"`

	// SpecPath overrides where deployment.yaml lives in the repository.
	SpecPath string `json:"specPath,omitempty"`

	// RootDir builds from a subdirectory, for monorepos.
	RootDir string `json:"rootDir,omitempty"`

	// AutoDeploy deploys on every push to the production branch.
	AutoDeploy bool `json:"autoDeploy"`

	// Domains are custom hostnames configured in the dashboard. They are
	// merged with any declared in deployment.yaml.
	Domains []string `json:"domains,omitempty"`

	// Env are project-level environment variables set in the dashboard.
	// Values marked secret are never returned by the API.
	Env []EnvVar `json:"env,omitempty"`

	// Current is the deployment currently serving traffic.
	Current string `json:"current,omitempty"`

	// Previous is the deployment that was live before Current. Its image
	// is the one kept for an instant rollback.
	Previous string `json:"previous,omitempty"`

	// Deployments is the retained history, newest first.
	Deployments []*Deployment `json:"deployments,omitempty"`

	// Paused stops webhooks and cron from acting on this project.
	Paused bool `json:"paused,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Repo identifies a GitHub repository.
type Repo struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	Branch   string `json:"branch"`
	CloneURL string `json:"cloneUrl"`
	Private  bool   `json:"private"`
	// HookID is the webhook publix created, so it can be removed when the
	// project is deleted rather than left rotting on the repository.
	HookID int64 `json:"hookId,omitempty"`
}

// FullName is "owner/name".
func (r *Repo) FullName() string { return r.Owner + "/" + r.Name }

// EnvVar is one environment variable configured on a project.
type EnvVar struct {
	Key string `json:"key"`
	// Value is the plaintext value. It is omitted from API responses when
	// Secret is set.
	Value string `json:"value"`
	// Secret hides the value from the dashboard after it is saved.
	Secret bool `json:"secret,omitempty"`
}

// Deployment is one attempt to make a commit live.
type Deployment struct {
	ID     string `json:"id"`
	Number int    `json:"number"`

	Status DeployStatus `json:"status"`
	// Trigger records who or what asked for this deployment.
	Trigger string `json:"trigger"`

	Commit  string `json:"commit,omitempty"`
	Short   string `json:"short,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Message string `json:"message,omitempty"`
	Author  string `json:"author,omitempty"`

	// Image is the tag built for this deployment. It may have been pruned;
	// ImagePresent records whether it is still on disk, which is what
	// decides between an instant rollback and a rebuild.
	Image string `json:"image,omitempty"`

	// Kind is how this deployment was built, copied from the resolved spec.
	Kind string `json:"kind,omitempty"`

	// Spec is the resolved deployment.yaml, as YAML. Keeping it is what
	// lets a rollback restore the configuration of that commit, not just
	// its code.
	Spec string `json:"spec,omitempty"`

	Error string `json:"error,omitempty"`

	QueuedAt   time.Time  `json:"queuedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// RolledBackFrom names the deployment this one was created to undo.
	RolledBackFrom string `json:"rolledBackFrom,omitempty"`
}

// DeployStatus is the lifecycle state of a deployment.
type DeployStatus string

// Deployment statuses.
const (
	StatusQueued     DeployStatus = "queued"
	StatusBuilding   DeployStatus = "building"
	StatusDeploying  DeployStatus = "deploying"
	StatusLive       DeployStatus = "live"
	StatusFailed     DeployStatus = "failed"
	StatusSuperseded DeployStatus = "superseded"
	StatusCancelled  DeployStatus = "cancelled"
)

// Terminal reports whether a deployment has finished, one way or another.
func (s DeployStatus) Terminal() bool {
	switch s {
	case StatusLive, StatusFailed, StatusSuperseded, StatusCancelled:
		return true
	}
	return false
}

// Duration is how long a finished deployment took.
func (d *Deployment) Duration() time.Duration {
	if d.StartedAt == nil {
		return 0
	}
	end := time.Now()
	if d.FinishedAt != nil {
		end = *d.FinishedAt
	}
	return end.Sub(*d.StartedAt)
}

// Deployment finds one of a project's deployments by ID.
func (p *Project) Deployment(id string) (*Deployment, bool) {
	for _, d := range p.Deployments {
		if d.ID == id {
			return d, true
		}
	}
	return nil, false
}

// LiveDeployment returns the deployment currently serving traffic.
func (p *Project) LiveDeployment() *Deployment {
	if p.Current == "" {
		return nil
	}
	d, _ := p.Deployment(p.Current)
	return d
}

// SortDeployments orders history newest first.
func (p *Project) SortDeployments() {
	sort.SliceStable(p.Deployments, func(i, j int) bool {
		return p.Deployments[i].Number > p.Deployments[j].Number
	})
}

// NextDeploymentNumber returns the sequence number for a new deployment.
func (p *Project) NextDeploymentNumber() int {
	n := 0
	for _, d := range p.Deployments {
		if d.Number > n {
			n = d.Number
		}
	}
	return n + 1
}

// EnvMap flattens a project's environment variables.
func (p *Project) EnvMap() map[string]string {
	out := make(map[string]string, len(p.Env))
	for _, e := range p.Env {
		out[e.Key] = e.Value
	}
	return out
}

// Redacted returns a copy of the project safe to send to a browser: secret
// values are replaced, never merely hidden by the client.
func (p *Project) Redacted() *Project {
	clone := *p
	clone.Env = make([]EnvVar, len(p.Env))
	for i, e := range p.Env {
		if e.Secret {
			e.Value = ""
		}
		clone.Env[i] = e
	}
	if p.Repo != nil {
		r := *p.Repo
		clone.Repo = &r
	}
	return &clone
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify converts a project or repository name into a DNS label.
func Slugify(s string) string {
	out := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if out == "" {
		out = "project"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// NewID generates a short opaque identifier. Eight hex characters is 4
// bytes of entropy: ample for the number of projects one host will ever
// hold, and short enough to appear in a directory name without noise.
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition a deploy tool can
		// meaningfully recover from, and a time-based fallback would
		// silently weaken every identifier and session key on the host.
		panic("publix: system randomness unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// NewToken generates a long random token for secrets and session keys.
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("publix: system randomness unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
