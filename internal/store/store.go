package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the platform's persistent state.
//
// Every mutation goes through Update, which takes the lock, applies a
// change, and writes the whole document atomically. That is slower than a
// database and completely adequate: the document is small, writes happen at
// human frequency, and the operational cost of "it is one JSON file you can
// read, back up and edit" is hard to beat for a self-hosted tool.
type Store struct {
	mu   sync.RWMutex
	path string
	data Data

	// listeners receive a notification after every committed change, which
	// is what drives live updates in the dashboard.
	listeners map[int]chan struct{}
	nextID    int
}

// Data is the on-disk document.
type Data struct {
	Version  int        `json:"version"`
	Settings Settings   `json:"settings"`
	Projects []*Project `json:"projects"`
}

// Open loads the store from the publix home directory.
func Open() (*Store, error) { return OpenAt(filepath.Join(Home(), "publix.json")) }

// OpenAt loads a store from an explicit path, creating it if absent.
func OpenAt(path string) (*Store, error) {
	s := &Store{
		path:      path,
		data:      Data{Version: 1, Settings: DefaultSettings(), Projects: []*Project{}},
		listeners: map[int]chan struct{}{},
	}

	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err), err == nil && len(raw) == 0:
		// A brand-new store still has to satisfy the invariants normalise
		// establishes — notably a webhook secret, without which incoming
		// webhooks are rejected and the settings page shows a blank field
		// the operator is meant to paste into GitHub.
		s.normalise()
		if err := s.persist(); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, err
	}

	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("%s is corrupt: %w\n\nMove it aside to start fresh. Running containers are not affected, but publix will no longer know about them.", path, err)
	}
	s.normalise()
	return s, nil
}

// normalise fills in fields added since the document was written, so an
// upgrade never requires a migration step from the operator.
func (s *Store) normalise() {
	def := DefaultSettings()
	set := &s.data.Settings
	if set.Network == "" {
		set.Network = def.Network
	}
	if set.TraefikDynamicDir == "" {
		set.TraefikDynamicDir = def.TraefikDynamicDir
	}
	if len(set.EntryPoints) == 0 {
		set.EntryPoints = def.EntryPoints
	}
	if set.WorkDir == "" {
		set.WorkDir = def.WorkDir
	}
	if set.KeepImages < 1 {
		set.KeepImages = def.KeepImages
	}
	if set.KeepDeployments < 1 {
		set.KeepDeployments = def.KeepDeployments
	}
	if set.BuildConcurrency < 1 {
		set.BuildConcurrency = def.BuildConcurrency
	}
	if set.Auth.SessionKey == "" {
		set.Auth.SessionKey = NewToken()
	}
	if set.GitHub.WebhookSecret == "" {
		set.GitHub.WebhookSecret = NewToken()
	}

	// Volumes gained a scope. Everything registered before that was
	// per-project, so migrate rather than silently changing what an
	// existing install's projects mount.
	if len(set.LegacySharedVolumes) > 0 {
		for _, v := range set.LegacySharedVolumes {
			if _, exists := set.Volume(v.Name); exists {
				continue
			}
			v.Scope = ScopeProject
			set.Volumes = append(set.Volumes, v)
		}
		set.LegacySharedVolumes = nil
	}
	for i := range set.Volumes {
		if set.Volumes[i].Scope == "" {
			set.Volumes[i].Scope = ScopeProject
		}
	}
	for _, p := range s.data.Projects {
		if p.Slug == "" {
			p.Slug = Slugify(p.Name)
		}
		p.SortDeployments()
	}
}

// Path reports where the store is persisted.
func (s *Store) Path() string { return s.path }

// persist writes the document. The caller must hold the write lock.
func (s *Store) persist() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	// 0600: this document holds secrets and GitHub tokens.
	return WriteFileAtomic(s.path, append(raw, '\n'), 0o600)
}

// Update applies fn under the write lock and persists the result. If fn
// returns an error nothing is written, so a rejected change cannot leave
// the document half-modified.
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	if err := fn(&s.data); err != nil {
		s.mu.Unlock()
		return err
	}
	err := s.persist()
	s.mu.Unlock()
	if err == nil {
		s.notify()
	}
	return err
}

// Read runs fn under the read lock. fn must not mutate what it is given.
func (s *Store) Read(fn func(*Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.data)
}

// Settings returns a copy of the server settings.
func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.data.Settings
	c.EntryPoints = append([]string(nil), c.EntryPoints...)
	c.Volumes = append([]Volume(nil), c.Volumes...)
	return c
}

// SetSettings replaces the server settings.
func (s *Store) SetSettings(fn func(*Settings) error) error {
	return s.Update(func(d *Data) error { return fn(&d.Settings) })
}

// Projects returns every project, newest first.
func (s *Store) Projects() []*Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Project, len(s.data.Projects))
	copy(out, s.data.Projects)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Project looks a project up by ID or slug, so URLs can carry either.
func (s *Store) Project(idOrSlug string) (*Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projectLocked(idOrSlug)
}

func (s *Store) projectLocked(idOrSlug string) (*Project, bool) {
	for _, p := range s.data.Projects {
		if p.ID == idOrSlug {
			return p, true
		}
	}
	for _, p := range s.data.Projects {
		if strings.EqualFold(p.Slug, idOrSlug) {
			return p, true
		}
	}
	return nil, false
}

// ProjectByRepo finds the project deploying a given repository and branch.
// It is how a webhook payload is turned into something to deploy.
func (s *Store) ProjectByRepo(owner, name, branch string) (*Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.data.Projects {
		if p.Repo == nil {
			continue
		}
		if !strings.EqualFold(p.Repo.Owner, owner) || !strings.EqualFold(p.Repo.Name, name) {
			continue
		}
		if branch == "" || p.Repo.Branch == "" || p.Repo.Branch == branch {
			return p, true
		}
	}
	return nil, false
}

// CreateProject registers a new project, assigning it an ID and a unique slug.
func (s *Store) CreateProject(p *Project) (*Project, error) {
	err := s.Update(func(d *Data) error {
		if p.Name == "" {
			return fmt.Errorf("a project needs a name")
		}
		if p.ID == "" {
			p.ID = NewID()
		}
		for _, existing := range d.Projects {
			if existing.ID == p.ID {
				return fmt.Errorf("project %s already exists", p.ID)
			}
		}
		p.Slug = uniqueSlug(d.Projects, Slugify(firstNonEmpty(p.Slug, p.Name)), "")
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		d.Projects = append(d.Projects, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateProject mutates one project and persists the change.
func (s *Store) UpdateProject(idOrSlug string, fn func(*Project) error) (*Project, error) {
	var out *Project
	err := s.Update(func(d *Data) error {
		p, ok := s.projectLocked(idOrSlug)
		if !ok {
			return &NotFoundError{Kind: "project", ID: idOrSlug}
		}
		if err := fn(p); err != nil {
			return err
		}
		p.Slug = uniqueSlug(d.Projects, Slugify(firstNonEmpty(p.Slug, p.Name)), p.ID)
		p.UpdatedAt = time.Now().UTC()
		out = p
		return nil
	})
	return out, err
}

// DeleteProject removes a project record. Its containers, images and
// shared-volume data are the caller's responsibility: this only forgets it.
func (s *Store) DeleteProject(idOrSlug string) error {
	return s.Update(func(d *Data) error {
		p, ok := s.projectLocked(idOrSlug)
		if !ok {
			return &NotFoundError{Kind: "project", ID: idOrSlug}
		}
		out := d.Projects[:0]
		for _, x := range d.Projects {
			if x.ID != p.ID {
				out = append(out, x)
			}
		}
		d.Projects = out
		return nil
	})
}

// AddDeployment records a new deployment at the head of a project's history
// and trims the history to the configured retention.
func (s *Store) AddDeployment(projectID string, dep *Deployment) error {
	return s.Update(func(d *Data) error {
		p, ok := s.projectLocked(projectID)
		if !ok {
			return &NotFoundError{Kind: "project", ID: projectID}
		}
		if dep.ID == "" {
			dep.ID = NewID()
		}
		if dep.Number == 0 {
			dep.Number = p.NextDeploymentNumber()
		}
		if dep.QueuedAt.IsZero() {
			dep.QueuedAt = time.Now().UTC()
		}
		p.Deployments = append([]*Deployment{dep}, p.Deployments...)
		p.SortDeployments()
		if n := d.Settings.KeepDeployments; n > 0 && len(p.Deployments) > n {
			p.Deployments = p.Deployments[:n]
		}
		p.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// UpdateDeployment mutates one deployment record.
func (s *Store) UpdateDeployment(projectID, deploymentID string, fn func(*Deployment)) error {
	return s.Update(func(d *Data) error {
		p, ok := s.projectLocked(projectID)
		if !ok {
			return &NotFoundError{Kind: "project", ID: projectID}
		}
		dep, ok := p.Deployment(deploymentID)
		if !ok {
			return &NotFoundError{Kind: "deployment", ID: deploymentID}
		}
		fn(dep)
		p.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// Promote makes a deployment the live one, shifting the outgoing deployment
// into the Previous slot. Previous is what the retained second image backs,
// so this is also what decides which image survives pruning.
func (s *Store) Promote(projectID, deploymentID string) error {
	return s.Update(func(d *Data) error {
		p, ok := s.projectLocked(projectID)
		if !ok {
			return &NotFoundError{Kind: "project", ID: projectID}
		}
		if p.Current == deploymentID {
			return nil
		}
		if old, ok := p.Deployment(p.Current); ok && old.Status == StatusLive {
			old.Status = StatusSuperseded
		}
		if p.Current != "" {
			p.Previous = p.Current
		}
		p.Current = deploymentID
		if dep, ok := p.Deployment(deploymentID); ok {
			dep.Status = StatusLive
		}
		p.UpdatedAt = time.Now().UTC()
		return nil
	})
}

// Subscribe returns a channel notified after every committed change, plus a
// function to unsubscribe. The channel is buffered and lossy by design: a
// listener only needs to know that something changed, not what.
func (s *Store) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	ch := make(chan struct{}, 1)
	s.listeners[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.listeners[id]; ok {
			delete(s.listeners, id)
			close(c)
		}
	}
}

func (s *Store) notify() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.listeners {
		select {
		case ch <- struct{}{}:
		default: // a pending notification already says all there is to say
		}
	}
}

// NotFoundError is returned when a referenced entity does not exist.
type NotFoundError struct{ Kind, ID string }

func (e *NotFoundError) Error() string { return e.Kind + " " + e.ID + " not found" }

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}

// uniqueSlug appends a numeric suffix until the slug is free.
func uniqueSlug(projects []*Project, want, selfID string) string {
	taken := map[string]bool{}
	for _, p := range projects {
		if p.ID != selfID {
			taken[p.Slug] = true
		}
	}
	if !taken[want] {
		return want
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", want, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// WriteFileAtomic writes through a temp file and rename, so a reader never
// observes a partial document.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}
