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
	// byInstallation answers /installation/repositories per token, which is
	// how a real GitHub distinguishes one installation's world from
	// another's.
	byInstallation map[string]string
	// brokenInstallation refuses to mint a token, standing in for a
	// suspended or revoked installation.
	brokenInstallation string
	hits               map[string]int
}

// multi turns the stand-in into an App installed on two accounts, which is
// the ordinary case for anyone with a personal account and an organisation.
func (s *appServer) multi() {
	s.installations = `[
		{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected",
		 "html_url":"https://github.com/organizations/acme/settings/installations/42"},
		{"id":43,"account":{"login":"personal","type":"User"},"repository_selection":"all",
		 "html_url":"https://github.com/settings/installations/43"}]`
	s.byInstallation = map[string]string{
		"ghs_42": `{"repositories":[{"id":7,"name":"web","full_name":"acme/web","owner":{"login":"acme"}}]}`,
		"ghs_43": `{"repositories":[{"id":9,"name":"blog","full_name":"personal/blog","owner":{"login":"personal"}}]}`,
	}
}

func newAppServer(t *testing.T) *appServer {
	t.Helper()
	s := &appServer{hits: map[string]int{}}
	s.installations = `[{"id":42,"account":{"login":"acme","avatar_url":"https://example.test/a.png","type":"Organization"},
		"repository_selection":"selected","html_url":"https://github.com/organizations/acme/settings/installations/42"}]`
	s.installation = `{"id":42,"account":{"login":"acme","avatar_url":"https://example.test/a.png","type":"Organization"},
		"repository_selection":"selected","html_url":"https://github.com/organizations/acme/settings/installations/42"}`
	s.repositories = `{"total_count":1,"repositories":[
		{"id":7,"name":"web","full_name":"acme/web","owner":{"login":"acme"},"default_branch":"main"}]}`

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		if id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/app/installations/"), "/access_tokens"); ok && id != r.URL.Path {
			if id == s.brokenInstallation {
				http.Error(w, `{"message":"this installation has been suspended"}`, http.StatusForbidden)
				return
			}
			w.Write([]byte(`{"token":"ghs_` + id + `","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}
		switch {
		case r.URL.Path == "/app/installations":
			w.Write([]byte(s.installations))
		case r.URL.Path == "/app/installations/43":
			w.Write([]byte(`{"id":43,"account":{"login":"personal","type":"User"},"repository_selection":"all",
				"html_url":"https://github.com/settings/installations/43"}`))
		case strings.HasPrefix(r.URL.Path, "/app/installations/"):
			w.Write([]byte(s.installation))
		case r.URL.Path == "/installation/repositories":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if body, ok := s.byInstallation[tok]; ok {
				w.Write([]byte(body))
				return
			}
			w.Write([]byte(s.repositories))
		case r.URL.Path == "/repos/personal/blog":
			w.Write([]byte(`{"id":9,"name":"blog","full_name":"personal/blog","owner":{"login":"personal"}}`))
		default:
			http.Error(w, `{"message":"unexpected `+r.URL.Path+`"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func appClient(t *testing.T, base string) *Client { return appClientPinned(t, base, "") }

func appClientPinned(t *testing.T, base, installation string) *Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	c, err := New(store.GitHubSettings{
		AppID: "123", PrivateKey: string(pemKey), APIBase: base, InstallationID: installation,
	})
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

// An App installed on several accounts is normal — a personal account and
// a couple of organisations. publix used to refuse to act at all in that
// case, so connecting the App appeared to work and then showed no
// repositories, with the reason buried in an error nobody saw.
func TestReposComeFromEveryInstallation(t *testing.T) {
	srv := newAppServer(t)
	srv.multi()
	c := appClient(t, srv.URL)

	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, r := range repos {
		owners[r.Owner] = true
	}
	for _, want := range []string{"acme", "personal"} {
		if !owners[want] {
			t.Errorf("no repository from %s; got %+v", want, repos)
		}
	}
}

// Each installation is a separate credential. A repository owned by one
// account must be fetched with that account's token, or GitHub answers 404
// for a repository publix can plainly see in its own list.
func TestPerRepositoryCallsUseTheOwnersInstallation(t *testing.T) {
	srv := newAppServer(t)
	srv.multi()
	c := appClient(t, srv.URL)

	if _, err := c.GetRepo(context.Background(), "personal", "blog"); err != nil {
		t.Fatal(err)
	}
	if got := srv.hits["/app/installations/43/access_tokens"]; got != 1 {
		t.Errorf("minted %d tokens for installation 43, want 1 — the request used the wrong installation", got)
	}
	if got := srv.hits["/app/installations/42/access_tokens"]; got != 0 {
		t.Errorf("minted a token for acme's installation to read a repository owned by personal")
	}
}

// A clone URL is the other place an owner decides which installation can
// read the repository, and it is not a request path.
func TestCloneURLIsAuthenticatedAsTheOwnersInstallation(t *testing.T) {
	srv := newAppServer(t)
	srv.multi()
	c := appClient(t, srv.URL)

	got, err := c.AuthenticateCloneURL(context.Background(), "https://github.com/personal/blog.git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ghs_43") {
		t.Errorf("clone URL carries the wrong installation's token: %s", strings.ReplaceAll(got, "ghs_", "<token "))
	}
}

// Naming an installation explicitly still narrows publix to that one.
func TestPinnedInstallationIsTheOnlyOneUsed(t *testing.T) {
	srv := newAppServer(t)
	srv.multi()
	c := appClientPinned(t, srv.URL, "43")

	insts, isApp, err := c.Installations(context.Background())
	if err != nil || !isApp {
		t.Fatalf("isApp=%v err=%v", isApp, err)
	}
	if len(insts) != 1 || insts[0].Account.Login != "personal" {
		t.Fatalf("pinned to 43 but got %+v", insts)
	}
	if srv.hits["/app/installations"] != 0 {
		t.Error("listed every installation even though one was pinned")
	}
}

// One unreadable account — a suspended installation, say — must not blank
// out the repositories of the others.
func TestOneBrokenInstallationDoesNotHideTheRest(t *testing.T) {
	srv := newAppServer(t)
	srv.multi()
	srv.brokenInstallation = "43"
	c := appClient(t, srv.URL)

	repos, err := c.ListRepos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) == 0 {
		t.Fatal("acme's repositories disappeared because personal's installation failed")
	}
	for _, r := range repos {
		if r.Owner == "personal" {
			t.Errorf("returned a repository from the broken installation: %+v", r)
		}
	}
}
