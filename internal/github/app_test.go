package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Oein/publix/internal/store"
)

// appServer is a GitHub stand-in that answers the four endpoints App-mode
// authentication touches.
type appServer struct {
	*httptest.Server
	installations string
	installation  string
	repositories  string
	hits          map[string]int
}

func newAppServer(t *testing.T) *appServer {
	t.Helper()
	s := &appServer{hits: map[string]int{}}
	s.installations = `[{"id":42,"account":{"login":"acme"}}]`
	s.installation = `{"id":42,"account":{"login":"acme","avatar_url":"https://example.test/a.png","type":"Organization"},
		"repository_selection":"selected","html_url":"https://github.com/organizations/acme/settings/installations/42"}`
	s.repositories = `{"total_count":1,"repositories":[
		{"id":7,"name":"web","full_name":"acme/web","owner":{"login":"acme"},"default_branch":"main"}]}`

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/app/installations":
			w.Write([]byte(s.installations))
		case r.URL.Path == "/app/installations/42/access_tokens":
			w.Write([]byte(`{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`))
		case r.URL.Path == "/app/installations/42":
			w.Write([]byte(s.installation))
		case r.URL.Path == "/installation/repositories":
			w.Write([]byte(s.repositories))
		default:
			http.Error(w, `{"message":"unexpected `+r.URL.Path+`"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func appClient(t *testing.T, base string) *Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	c, err := New(store.GitHubSettings{AppID: "123", PrivateKey: string(pemKey), APIBase: base})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Whoami used to decode the installation into a throwaway value and return
// a blank login, so the settings page said "connected" without saying to
// what — which is the one fact that explains an empty repository list.
func TestWhoamiNamesTheInstallationAccount(t *testing.T) {
	srv := newAppServer(t)
	c := appClient(t, srv.URL)

	viewer, err := c.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if viewer.Login != "acme" {
		t.Errorf("login = %q, want %q", viewer.Login, "acme")
	}
	if viewer.Type != "Installation" {
		t.Errorf("type = %q, want Installation", viewer.Type)
	}
}

func TestCurrentInstallationReportsRepositoryAccess(t *testing.T) {
	srv := newAppServer(t)
	c := appClient(t, srv.URL)

	inst, isApp, err := c.CurrentInstallation(context.Background())
	if err != nil || !isApp {
		t.Fatalf("CurrentInstallation: isApp=%v err=%v", isApp, err)
	}
	if inst.RepositorySelection != "selected" {
		t.Errorf("repository selection = %q, want selected", inst.RepositorySelection)
	}
	if inst.HTMLURL == "" {
		t.Error("no settings URL, so the dashboard cannot link an operator to the fix")
	}
}

// A token-mode client has no installation to describe, and must say so
// rather than reaching for App endpoints it cannot authenticate to.
func TestCurrentInstallationIsAppOnly(t *testing.T) {
	c, err := New(store.GitHubSettings{Token: "ghp_test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, isApp, err := c.CurrentInstallation(context.Background()); isApp || err != nil {
		t.Fatalf("isApp=%v err=%v, want false and no error", isApp, err)
	}
}

func TestListReposReadsTheInstallationsRepositories(t *testing.T) {
	srv := newAppServer(t)
	c := appClient(t, srv.URL)

	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "acme/web" {
		t.Fatalf("repos = %+v, want one acme/web", repos)
	}
	if repos[0].Owner != "acme" {
		t.Errorf("owner = %q, want acme", repos[0].Owner)
	}
}

// An installation granted no repositories is the case that looks like a
// broken connection. It has to come back empty and without an error, so the
// dashboard can explain it rather than showing a GitHub failure.
func TestEmptyInstallationIsNotAnError(t *testing.T) {
	srv := newAppServer(t)
	srv.repositories = `{"total_count":0,"repositories":[]}`
	c := appClient(t, srv.URL)

	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("repos = %+v, want none", repos)
	}
}

// The installation is discovered once. Re-listing it on every token refresh
// would spend a request per call for an answer that cannot change.
func TestInstallationIsDiscoveredOnce(t *testing.T) {
	srv := newAppServer(t)
	c := appClient(t, srv.URL)
	ctx := context.Background()

	if _, err := c.ListRepos(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.CurrentInstallation(ctx); err != nil {
		t.Fatal(err)
	}
	if got := srv.hits["/app/installations"]; got != 1 {
		t.Errorf("listed installations %d times, want 1", got)
	}
}

// With more than one installation publix cannot pick for the operator, and
// the error has to name the choices rather than failing opaquely.
func TestAmbiguousInstallationNamesTheOptions(t *testing.T) {
	srv := newAppServer(t)
	srv.installations = `[{"id":42,"account":{"login":"acme"}},{"id":43,"account":{"login":"personal"}}]`
	c := appClient(t, srv.URL)

	_, err := c.ListRepos(context.Background())
	if err == nil {
		t.Fatal("want an error naming both installations")
	}
	for _, want := range []string{"acme", "personal", "43"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
