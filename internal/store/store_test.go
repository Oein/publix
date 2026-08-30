package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAt(filepath.Join(t.TempDir(), "publix.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A shared volume registration decides what host paths projects can reach,
// so the checks around it are security-relevant, not cosmetic.
func TestValidateVolumeRejectsDangerousPaths(t *testing.T) {
	s := &Settings{}
	for _, path := range []string{
		"/", "/etc", "/usr", "/var/run", "/root", "/proc", "/sys", "/dev",
		"relative/path", "/mnt/../etc",
	} {
		err := s.ValidateVolume(SharedVolume{Name: "disk0", Path: path}, "")
		if err == nil {
			t.Errorf("path %q should have been rejected", path)
		}
	}

	if err := s.ValidateVolume(SharedVolume{Name: "disk0", Path: "/mnt/data"}, ""); err != nil {
		t.Errorf("a normal path was rejected: %v", err)
	}
}

func TestValidateVolumeRejectsBadNames(t *testing.T) {
	s := &Settings{}
	for _, name := range []string{"", "Disk0", "disk 0", "../escape", strings.Repeat("x", 64)} {
		if err := s.ValidateVolume(SharedVolume{Name: name, Path: "/mnt/data"}, ""); err == nil {
			t.Errorf("name %q should have been rejected", name)
		}
	}
}

func TestValidateVolumeRejectsDuplicateName(t *testing.T) {
	s := &Settings{SharedVolumes: []SharedVolume{{Name: "disk0", Path: "/mnt/a"}}}

	if err := s.ValidateVolume(SharedVolume{Name: "disk0", Path: "/mnt/b"}, ""); err == nil {
		t.Error("a duplicate volume name should be rejected")
	}
	// Editing the existing one in place is not a duplicate.
	if err := s.ValidateVolume(SharedVolume{Name: "disk0", Path: "/mnt/b"}, "disk0"); err != nil {
		t.Errorf("editing a volume in place should be allowed: %v", err)
	}
}

// Two projects mounting one volume must land in different directories.
// This is the whole isolation guarantee.
func TestSharedVolumeDirectoryIsPerProject(t *testing.T) {
	v := SharedVolume{Name: "disk0", Path: "/mnt/data"}
	a, b := v.ProjectDir("aaaa1111"), v.ProjectDir("bbbb2222")
	if a == b {
		t.Fatal("two projects resolved to the same directory")
	}
	if a != "/mnt/data/aaaa1111" {
		t.Errorf("ProjectDir = %q, want <path>/<project id>", a)
	}
	if v.Mount() != "/shared/disk0" {
		t.Errorf("Mount = %q, want /shared/<name>", v.Mount())
	}
}

func TestSlugsAreUnique(t *testing.T) {
	s := open(t)
	for _, name := range []string{"My App", "my-app", "my app"} {
		if _, err := s.CreateProject(&Project{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, p := range s.Projects() {
		if seen[p.Slug] {
			t.Errorf("slug %q was assigned twice", p.Slug)
		}
		seen[p.Slug] = true
	}
	if len(seen) != 3 {
		t.Errorf("got %d distinct slugs, want 3", len(seen))
	}
}

// Promote is what decides which two images survive pruning, so the Previous
// slot has to track the outgoing deployment exactly.
func TestPromoteTracksPrevious(t *testing.T) {
	s := open(t)
	p, err := s.CreateProject(&Project{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d1", "d2", "d3"} {
		if err := s.AddDeployment(p.ID, &Deployment{ID: id}); err != nil {
			t.Fatal(err)
		}
		if err := s.Promote(p.ID, id); err != nil {
			t.Fatal(err)
		}
	}

	got, _ := s.Project(p.ID)
	if got.Current != "d3" || got.Previous != "d2" {
		t.Errorf("current=%q previous=%q, want d3/d2", got.Current, got.Previous)
	}
	if d, _ := got.Deployment("d2"); d.Status != StatusSuperseded {
		t.Errorf("the outgoing deployment is %q, want superseded", d.Status)
	}
	if d, _ := got.Deployment("d3"); d.Status != StatusLive {
		t.Errorf("the promoted deployment is %q, want live", d.Status)
	}
}

// Secret values must never leave the server.
func TestRedactedHidesSecretValues(t *testing.T) {
	p := &Project{
		Name: "app",
		Env: []EnvVar{
			{Key: "PUBLIC", Value: "visible"},
			{Key: "TOKEN", Value: "s3cret", Secret: true},
		},
	}
	r := p.Redacted()
	for _, e := range r.Env {
		if e.Secret && e.Value != "" {
			t.Errorf("secret %s leaked its value", e.Key)
		}
		if !e.Secret && e.Value == "" {
			t.Errorf("non-secret %s lost its value", e.Key)
		}
	}
	// The original must be untouched, or the next deploy would inject a
	// blank value for every secret.
	if p.Env[1].Value != "s3cret" {
		t.Error("Redacted mutated the project it was called on")
	}
}

func TestDeploymentHistoryIsTrimmed(t *testing.T) {
	s := open(t)
	if err := s.SetSettings(func(set *Settings) error {
		set.KeepDeployments = 3
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.CreateProject(&Project{Name: "app"})
	for i := 0; i < 10; i++ {
		if err := s.AddDeployment(p.ID, &Deployment{}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Project(p.ID)
	if len(got.Deployments) != 3 {
		t.Errorf("kept %d deployments, want 3", len(got.Deployments))
	}
	// Newest first.
	if got.Deployments[0].Number != 10 {
		t.Errorf("head of history is #%d, want the newest (#10)", got.Deployments[0].Number)
	}
}

func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publix.json")

	first, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := first.CreateProject(&Project{Name: "app", Domains: []string{"a.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	second, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := second.Project(p.ID)
	if !ok {
		t.Fatal("the project did not survive a reopen")
	}
	if got.Name != "app" || len(got.Domains) != 1 {
		t.Errorf("reloaded project is wrong: %+v", got)
	}
}

// A URL or slug can address a project; both must resolve.
func TestProjectLookupByIDAndSlug(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject(&Project{Name: "My App"})
	for _, key := range []string{p.ID, p.Slug, strings.ToUpper(p.Slug)} {
		if _, ok := s.Project(key); !ok {
			t.Errorf("lookup by %q failed", key)
		}
	}
	if _, ok := s.Project("nope"); ok {
		t.Error("an unknown key resolved to a project")
	}
}
