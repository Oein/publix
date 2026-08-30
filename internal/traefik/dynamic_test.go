package traefik

import (
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
)

func settings() *store.Settings {
	return &store.Settings{
		Network:      "publix",
		EntryPoints:  []string{"websecure"},
		CertResolver: "letsencrypt",
		AppsDomain:   "apps.example.com",
	}
}

func project(slug string, domains ...string) *store.Project {
	return &store.Project{ID: "abcd1234", Slug: slug, Name: slug, Domains: domains, CreatedAt: time.Now()}
}

func spec(t *testing.T, yaml string) *deployspec.Spec {
	t.Helper()
	sp, err := deployspec.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// The central claim of the design: a production hostname points at a
// deployment-scoped service name, so moving traffic is a change to this
// file and nothing else.
func TestProductionRouterTargetsDeploymentService(t *testing.T) {
	set := settings()
	d := Build(set, []Live{{
		Project:    project("api", "api.example.com"),
		Spec:       spec(t, "port: 8080\n"),
		Deployment: "dep1",
	}})

	var found *Router
	for _, r := range d.HTTP.Routers {
		if strings.Contains(r.Rule, "api.example.com") {
			found = r
		}
	}
	if found == nil {
		t.Fatal("no router for the custom domain")
	}
	if found.Service != "publix-api-dep1@docker" {
		t.Errorf("service = %q, want the deployment-scoped docker service", found.Service)
	}
	if found.TLS == nil || found.TLS.CertResolver != "letsencrypt" {
		t.Errorf("TLS = %+v, want the configured cert resolver", found.TLS)
	}
}

// Rebuilding after a promotion must move every hostname at once.
func TestCutoverRepointsEveryHost(t *testing.T) {
	set := settings()
	p := project("api", "api.example.com", "www.api.example.com")
	sp := spec(t, "port: 8080\n")

	before := Build(set, []Live{{Project: p, Spec: sp, Deployment: "old"}})
	after := Build(set, []Live{{Project: p, Spec: sp, Deployment: "new"}})

	if len(before.HTTP.Routers) != len(after.HTTP.Routers) {
		t.Fatalf("router count changed across a cutover: %d -> %d", len(before.HTTP.Routers), len(after.HTTP.Routers))
	}
	for name, r := range after.HTTP.Routers {
		if !strings.HasSuffix(r.Service, "publix-api-new@docker") {
			t.Errorf("router %s still points at %q after cutover", name, r.Service)
		}
	}
	// Three hosts: two custom plus the generated <slug>.<appsDomain>.
	if len(after.HTTP.Routers) != 3 {
		t.Errorf("got %d routers, want 3 (two custom domains and the generated one)", len(after.HTTP.Routers))
	}
}

// With nothing live, emitting a router pointing at a non-existent service
// would misrepresent the platform's state.
func TestNoLiveDeploymentEmitsNoRouter(t *testing.T) {
	d := Build(settings(), []Live{{
		Project: project("api", "api.example.com"),
		Spec:    spec(t, "port: 8080\n"),
	}})
	if len(d.HTTP.Routers) != 0 {
		t.Errorf("got %d routers with nothing live, want 0", len(d.HTTP.Routers))
	}
}

func TestGeneratedProjectHost(t *testing.T) {
	d := Build(settings(), []Live{{
		Project:    project("api"),
		Spec:       spec(t, "port: 8080\n"),
		Deployment: "dep1",
	}})
	var rules []string
	for _, r := range d.HTTP.Routers {
		rules = append(rules, r.Rule)
	}
	joined := strings.Join(rules, " ")
	if !strings.Contains(joined, "api.apps.example.com") {
		t.Errorf("a project with no custom domain should still get a generated host; rules: %v", rules)
	}
}

func TestRedirectRouteUsesMiddlewareNotABackend(t *testing.T) {
	d := Build(settings(), []Live{{
		Project:    project("api"),
		Spec:       spec(t, "port: 8080\nroutes:\n  - domain: old.example.com\n    redirectTo: new.example.com\n"),
		Deployment: "dep1",
	}})

	var redirect *Router
	for _, r := range d.HTTP.Routers {
		if strings.Contains(r.Rule, "old.example.com") {
			redirect = r
		}
	}
	if redirect == nil {
		t.Fatal("no router for the redirect domain")
	}
	if len(redirect.Middlewares) != 1 {
		t.Fatalf("redirect router has %d middlewares, want 1", len(redirect.Middlewares))
	}
	mw := d.HTTP.Middlewares[redirect.Middlewares[0]]
	if mw == nil || mw.RedirectRegex == nil {
		t.Fatal("expected a redirectRegex middleware")
	}
	if !strings.Contains(mw.RedirectRegex.Replacement, "new.example.com") {
		t.Errorf("replacement = %q", mw.RedirectRegex.Replacement)
	}
	// A hostname's dots must be escaped, or the regex would match hosts
	// like "oldXexample.com".
	if !strings.Contains(mw.RedirectRegex.Regex, `old\.example\.com`) {
		t.Errorf("regex should escape dots in the hostname, got %q", mw.RedirectRegex.Regex)
	}
}

func TestPathRouteStripPrefix(t *testing.T) {
	d := Build(settings(), []Live{{
		Project:    project("api"),
		Spec:       spec(t, "port: 8080\nroutes:\n  - domain: example.com\n    path: /api\n    stripPath: true\n"),
		Deployment: "dep1",
	}})
	var r *Router
	for _, x := range d.HTTP.Routers {
		if strings.Contains(x.Rule, "example.com") && strings.Contains(x.Rule, "PathPrefix") {
			r = x
		}
	}
	if r == nil {
		t.Fatal("no path-prefixed router")
	}
	if !strings.Contains(r.Rule, "PathPrefix(`/api`)") {
		t.Errorf("rule = %q", r.Rule)
	}
	mw := d.HTTP.Middlewares[r.Middlewares[0]]
	if mw == nil || mw.StripPrefix == nil || mw.StripPrefix.Prefixes[0] != "/api" {
		t.Errorf("expected a stripPrefix middleware for /api, got %+v", mw)
	}
}

// The generated file must be byte-identical for identical input, or every
// unrelated deploy would rewrite it and make Traefik reload the whole host.
func TestRenderIsDeterministic(t *testing.T) {
	set := settings()
	live := []Live{
		{Project: project("api", "api.example.com"), Spec: spec(t, "port: 8080\n"), Deployment: "d1"},
		{Project: project("web", "web.example.com"), Spec: spec(t, "port: 3000\n"), Deployment: "d2"},
	}
	first, err := Build(set, live).Render()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, err := Build(set, live).Render()
		if err != nil {
			t.Fatal(err)
		}
		if string(first) != string(again) {
			t.Fatal("Render is not deterministic across runs")
		}
	}
}

func TestTLSDisabledWhenNoCertResolver(t *testing.T) {
	set := settings()
	set.CertResolver = ""
	d := Build(set, []Live{{
		Project:    project("api", "api.example.com"),
		Spec:       spec(t, "port: 8080\n"),
		Deployment: "dep1",
	}})
	for name, r := range d.HTTP.Routers {
		if r.TLS != nil {
			t.Errorf("router %s has TLS with no cert resolver configured", name)
		}
	}
}

func TestSlugCollisionResistance(t *testing.T) {
	long1 := strings.Repeat("feature-branch-", 5) + "one"
	long2 := strings.Repeat("feature-branch-", 5) + "two"
	if Slug(long1) == Slug(long2) {
		t.Errorf("two long distinct names collided onto %q", Slug(long1))
	}
	for _, s := range []string{long1, long2, "한글-브랜치", ""} {
		got := Slug(s)
		if len(got) > 63 || got == "" {
			t.Errorf("Slug(%q) = %q is not a usable DNS label", s, got)
		}
	}
}
