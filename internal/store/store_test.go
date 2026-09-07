package store

import (
	"os"
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
		// Everything *inside* a protected directory is protected too. Only
		// exact matches were refused before, so /etc/ssh sailed through.
		"/etc/ssh", "/root/.ssh", "/var/run/docker", "/usr/local/share",
		"/var/lib/docker/volumes",
	} {
		err := s.ValidateVolume(Volume{Name: "disk0", Path: path, Scope: ScopeProject}, "")
		if err == nil {
			t.Errorf("path %q should have been rejected", path)
		}
	}

	for _, path := range []string{"/mnt/data", "/srv/uploads", "/opt/publix-data", "/home/deploy/data"} {
		if err := s.ValidateVolume(Volume{Name: "disk0", Path: path, Scope: ScopeProject}, ""); err != nil {
			t.Errorf("a normal path %q was rejected: %v", path, err)
		}
	}
}

// publix's own state holds every project's secrets and the GitHub
// credentials, and the Traefik file decides what each hostname reaches. A
// project able to write either would own the platform.
func TestValidateVolumeProtectsPublixsOwnState(t *testing.T) {
	t.Setenv("PUBLIX_HOME", "/var/lib/publix")
	s := &Settings{
		WorkDir:           "/var/lib/publix/work",
		TraefikDynamicDir: "/etc/traefik/dynamic",
	}
	for _, path := range []string{
		"/var/lib/publix",
		"/var/lib/publix/work",
		"/var/lib/publix/logs",
		"/etc/traefik/dynamic",
	} {
		if err := s.ValidateVolume(Volume{Name: "d", Path: path, Scope: ScopeProject}, ""); err == nil {
			t.Errorf("path %q should have been rejected", path)
		}
	}
}

// A shared volume containing a project volume's root lets every project
// read every other project's directory, which is exactly what the scopes
// promise it cannot.
func TestValidateVolumeRejectsNesting(t *testing.T) {
	s := &Settings{Volumes: []Volume{{Name: "outer", Path: "/mnt/data", Scope: ScopeShared}}}

	for _, path := range []string{"/mnt/data", "/mnt/data/inner", "/mnt"} {
		if err := s.ValidateVolume(Volume{Name: "inner", Path: path, Scope: ScopeProject}, ""); err == nil {
			t.Errorf("path %q overlaps /mnt/data and should have been rejected", path)
		}
	}
	if err := s.ValidateVolume(Volume{Name: "inner", Path: "/mnt/other", Scope: ScopeProject}, ""); err != nil {
		t.Errorf("a sibling path was rejected: %v", err)
	}
	// Editing a volume in place must not collide with itself.
	if err := s.ValidateVolume(Volume{Name: "outer", Path: "/mnt/data", Scope: ScopeShared}, "outer"); err != nil {
		t.Errorf("editing a volume in place was rejected: %v", err)
	}
}

// A symlink into a protected directory is still that directory, and
// checking only the name someone typed would miss it.
func TestValidateVolumeFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "innocent")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	err := (&Settings{}).ValidateVolume(Volume{Name: "d", Path: link, Scope: ScopeProject}, "")
	if err == nil {
		t.Fatal("a symlink to /etc was accepted")
	}
	if !strings.Contains(err.Error(), "/etc") {
		t.Errorf("the error does not say where it actually points: %v", err)
	}
}

func TestValidateVolumeChecksMountAndScope(t *testing.T) {
	s := &Settings{}
	bad := map[string]Volume{
		"relative mount": {Name: "d", Path: "/mnt/d", Scope: ScopeProject, DefaultMount: "shared/d"},
		"mount over /":   {Name: "d", Path: "/mnt/d", Scope: ScopeProject, DefaultMount: "/"},
		"unknown scope":  {Name: "d", Path: "/mnt/d", Scope: VolumeScope("everyone")},
		"bad name":       {Name: "Disk 0", Path: "/mnt/d", Scope: ScopeProject},
		"no path":        {Name: "d", Scope: ScopeProject},
	}
	for name, v := range bad {
		if err := s.ValidateVolume(v, ""); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestValidateVolumeRejectsBadNames(t *testing.T) {
	s := &Settings{}
	for _, name := range []string{"", "Disk0", "disk 0", "../escape", strings.Repeat("x", 64)} {
		if err := s.ValidateVolume(Volume{Name: name, Path: "/mnt/data", Scope: ScopeProject}, ""); err == nil {
			t.Errorf("name %q should have been rejected", name)
		}
	}
}

func TestValidateVolumeRejectsDuplicateName(t *testing.T) {
	s := &Settings{Volumes: []Volume{{Name: "disk0", Path: "/mnt/a", Scope: ScopeProject}}}

	if err := s.ValidateVolume(Volume{Name: "disk0", Path: "/mnt/b", Scope: ScopeProject}, ""); err == nil {
		t.Error("a duplicate volume name should be rejected")
	}
	// Editing the existing one in place is not a duplicate.
	if err := s.ValidateVolume(Volume{Name: "disk0", Path: "/mnt/b", Scope: ScopeProject}, "disk0"); err != nil {
		t.Errorf("editing a volume in place should be allowed: %v", err)
	}
}

// A project-scoped volume must give two projects different directories.
// That is the isolation guarantee, and the reason it is the default.
func TestProjectScopedVolumeIsolatesProjects(t *testing.T) {
	v := Volume{Name: "disk0", Path: "/mnt/data", Scope: ScopeProject}

	a, b := v.Dir("aaaa1111"), v.Dir("bbbb2222")
	if a == b {
		t.Fatal("two projects resolved to the same directory")
	}
	if a != "/mnt/data/aaaa1111" {
		t.Errorf("Dir = %q, want <path>/<project id>", a)
	}
	if v.Mount() != "/shared/disk0" {
		t.Errorf("Mount = %q, want /shared/<name>", v.Mount())
	}
	if v.Shared() {
		t.Error("a project-scoped volume must not report itself shared")
	}
}

// A shared volume must give every project the same directory — that is the
// entire point of it, and the opposite of the guarantee above.
func TestSharedVolumeIsTheSameDirectoryForEveryone(t *testing.T) {
	v := Volume{Name: "media", Path: "/mnt/media", Scope: ScopeShared}

	a, b := v.Dir("aaaa1111"), v.Dir("bbbb2222")
	if a != b {
		t.Fatalf("a shared volume gave two projects different directories: %q and %q", a, b)
	}
	if a != "/mnt/media" {
		t.Errorf("Dir = %q, want the volume path itself", a)
	}
	if !v.Shared() {
		t.Error("Shared() should be true")
	}
}

// A volume registered before scopes existed was per-project, and must stay
// that way: silently promoting it to shared would expose every project's
// data to every other one on the next deploy.
func TestLegacyVolumesMigrateToProjectScope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publix.json")

	legacy := `{"version":1,"settings":{"sharedVolumes":[{"name":"disk0","path":"/mnt/data"}]},"projects":[]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	set := s.Settings()
	if len(set.Volumes) != 1 {
		t.Fatalf("got %d volumes after migration, want 1", len(set.Volumes))
	}
	v := set.Volumes[0]
	if v.Name != "disk0" || v.Path != "/mnt/data" {
		t.Errorf("volume was not carried over: %+v", v)
	}
	if v.Scope != ScopeProject {
		t.Errorf("scope = %q, want project — an existing volume must not become shared", v.Scope)
	}
	if v.Dir("abcd1234") != "/mnt/data/abcd1234" {
		t.Errorf("Dir = %q, want the per-project directory it had before", v.Dir("abcd1234"))
	}

	// And the migration must not run twice or duplicate on reopen.
	again, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Settings().Volumes) != 1 {
		t.Errorf("reopening duplicated volumes: %+v", again.Settings().Volumes)
	}
}

func TestVolumeScopeIsValidated(t *testing.T) {
	s := &Settings{}
	if err := s.ValidateVolume(Volume{Name: "x", Path: "/mnt/x", Scope: "everyone"}, ""); err == nil {
		t.Error("an unknown scope should be rejected")
	}
	for _, scope := range []VolumeScope{ScopeProject, ScopeShared} {
		if err := s.ValidateVolume(Volume{Name: "x", Path: "/mnt/x", Scope: scope}, ""); err != nil {
			t.Errorf("scope %q was rejected: %v", scope, err)
		}
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

// A fresh store must already satisfy every invariant, not acquire them the
// first time something happens to write settings. A missing webhook secret
// means incoming webhooks are refused and the settings page offers the
// operator a blank field to paste into GitHub.
func TestFreshStoreIsNormalised(t *testing.T) {
	s := open(t)
	set := s.Settings()

	if set.GitHub.WebhookSecret == "" {
		t.Error("a fresh store has no webhook secret")
	}
	if set.Auth.SessionKey == "" {
		t.Error("a fresh store has no session signing key")
	}
	if set.Network == "" || set.WorkDir == "" || set.KeepImages < 1 {
		t.Errorf("defaults missing on a fresh store: %+v", set)
	}

	// And they must survive a reopen rather than being regenerated, or
	// every restart would invalidate sessions and break existing webhooks.
	again, err := OpenAt(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	reopened := again.Settings()
	if reopened.GitHub.WebhookSecret != set.GitHub.WebhookSecret {
		t.Error("the webhook secret changed across a reopen; existing GitHub webhooks would break")
	}
	if reopened.Auth.SessionKey != set.Auth.SessionKey {
		t.Error("the session key changed across a reopen; everyone would be signed out")
	}
}

// A server used to have exactly one apps domain. Opening an older state
// file has to carry it into the list, or every project's generated address
// moves the moment publix is upgraded.
func TestLegacyAppsDomainBecomesTheDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "publix.json")
	if err := os.WriteFile(path, []byte(`{
		"settings": {"appsDomain": "apps.example.com"},
		"projects": [{"id":"abcd","slug":"blog","name":"blog"}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	set := s.Settings()

	if got := set.DefaultAppsDomain(); got != "apps.example.com" {
		t.Fatalf("default = %q, want apps.example.com", got)
	}
	if set.LegacyAppsDomain != "" {
		t.Error("the legacy field should be emptied once migrated, so it cannot drift from the list")
	}
	if len(set.AppsDomains) != 1 || !set.AppsDomains[0].Default {
		t.Fatalf("apps domains = %+v, want one marked default", set.AppsDomains)
	}
	if got := set.AppsDomainFor(s.Projects()[0]); got != "apps.example.com" {
		t.Errorf("the existing project moved to %q", got)
	}
}

func TestAppsDomainForResolvesAProjectsChoice(t *testing.T) {
	set := &Settings{AppsDomains: []AppsDomain{
		{Domain: "apps.example.com", Default: true},
		{Domain: "staging.example.com"},
	}}

	cases := []struct {
		name    string
		chosen  string
		want    string
		because string
	}{
		{"unset takes the default", "", "apps.example.com", ""},
		{"an explicit choice is honoured", "staging.example.com", "staging.example.com", ""},
		{"the sentinel opts out entirely", AppsDomainNone, "", ""},
		{
			"an unregistered choice falls back", "gone.example.com", "apps.example.com",
			"dropping the project off the internet is worse than moving it",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := set.AppsDomainFor(&Project{AppsDomain: c.chosen})
			if got != c.want {
				t.Errorf("got %q, want %q %s", got, c.want, c.because)
			}
		})
	}
}

// With nothing marked default the answer would depend on registration
// order, which is not a decision anyone made.
func TestDefaultAppsDomainFallsBackToTheFirst(t *testing.T) {
	set := &Settings{AppsDomains: []AppsDomain{{Domain: "a.example.com"}, {Domain: "b.example.com"}}}
	if got := set.DefaultAppsDomain(); got != "a.example.com" {
		t.Errorf("default = %q, want a.example.com", got)
	}
	if got := (&Settings{}).DefaultAppsDomain(); got != "" {
		t.Errorf("with none registered, default = %q, want empty", got)
	}
}

func TestNormaliseAppsDomain(t *testing.T) {
	ok := map[string]string{
		"apps.example.com":          "apps.example.com",
		"  APPS.Example.COM  ":      "apps.example.com",
		"*.apps.example.com":        "apps.example.com",
		"https://apps.example.com/": "apps.example.com",
		"apps.example.com.":         "apps.example.com",
	}
	for in, want := range ok {
		got, err := NormaliseAppsDomain(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
		} else if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{"", "example", "not a domain", "-bad.example.com", AppsDomainNone} {
		if got, err := NormaliseAppsDomain(bad); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}
