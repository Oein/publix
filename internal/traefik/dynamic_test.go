package traefik

import (
	"os"
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
		AppsDomains:  []store.AppsDomain{{Domain: "apps.example.com", Default: true}},
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

// The point of registering several parent domains: two projects on one
// server can sit under different ones.
func TestProjectsRouteUnderTheirOwnAppsDomain(t *testing.T) {
	set := settings()
	set.AppsDomains = append(set.AppsDomains, store.AppsDomain{Domain: "staging.example.com"})

	blog := project("blog")
	preview := project("preview")
	preview.AppsDomain = "staging.example.com"
	quiet := project("quiet")
	quiet.AppsDomain = store.AppsDomainNone

	d := Build(set, []Live{
		{Project: blog, Spec: spec(t, "port: 8080\n"), Deployment: "d1"},
		{Project: preview, Spec: spec(t, "port: 8080\n"), Deployment: "d2"},
		{Project: quiet, Spec: spec(t, "port: 8080\n"), Deployment: "d3"},
	})

	var rules []string
	for _, r := range d.HTTP.Routers {
		rules = append(rules, r.Rule)
	}
	joined := strings.Join(rules, " ")

	for _, want := range []string{"blog.apps.example.com", "preview.staging.example.com"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no router for %s; rules were %v", want, rules)
		}
	}
	if strings.Contains(joined, "quiet.") {
		t.Errorf("a project that opted out still got a generated hostname: %v", rules)
	}
}

// A project that opted out of a generated hostname still has to be
// reachable on the domains it does declare.
func TestOptingOutKeepsCustomDomains(t *testing.T) {
	set := settings()
	p := project("quiet", "quiet.example.com")
	p.AppsDomain = store.AppsDomainNone

	d := Build(set, []Live{{Project: p, Spec: spec(t, "port: 8080\n"), Deployment: "d1"}})

	var rules []string
	for _, r := range d.HTTP.Routers {
		rules = append(rules, r.Rule)
	}
	joined := strings.Join(rules, " ")
	if !strings.Contains(joined, "quiet.example.com") {
		t.Errorf("the custom domain was dropped along with the generated one: %v", rules)
	}
	if strings.Contains(joined, "apps.example.com") {
		t.Errorf("still routed under the apps domain: %v", rules)
	}
}

func redirectFor(t *testing.T, d *Dynamic, host string) (*Router, *Middleware) {
	t.Helper()
	for _, r := range d.HTTP.Routers {
		if !strings.Contains(r.Rule, "`"+host+"`") {
			continue
		}
		if len(r.Middlewares) == 0 {
			t.Fatalf("router for %s has no middleware, so nothing redirects", host)
		}
		return r, d.HTTP.Middlewares[r.Middlewares[0]]
	}
	t.Fatalf("no router for %s; routers were %v", host, d.HTTP.Routers)
	return nil, nil
}

func TestServerRedirectForwardsAHostWithNoProject(t *testing.T) {
	set := settings()
	set.Redirects = []store.Redirect{{Domain: "old.example.com", Target: "new.example.com"}}

	_, mw := redirectFor(t, Build(set, nil), "old.example.com")
	if mw.RedirectRegex == nil {
		t.Fatal("not a redirect")
	}
	if got := mw.RedirectRegex.Replacement; got != "https://new.example.com/${1}" {
		t.Errorf("replacement = %q, want the path carried across", got)
	}
	if mw.RedirectRegex.Permanent {
		t.Error("permanent by default: a 301 is cached forever and hard to take back")
	}
}

// Dropping the path is what someone means by "send everyone to this page",
// and a stray capture group would append the old path to it.
func TestRedirectCanDiscardThePath(t *testing.T) {
	keep := false
	set := settings()
	set.Redirects = []store.Redirect{{
		Domain: "old.example.com", Target: "https://example.com/moved", KeepPath: &keep, Permanent: true,
	}}

	_, mw := redirectFor(t, Build(set, nil), "old.example.com")
	if got := mw.RedirectRegex.Replacement; got != "https://example.com/moved" {
		t.Errorf("replacement = %q, want the target exactly — a capture group would append the old\n"+
			"path, and a trailing slash would turn /moved into /moved/", got)
	}
	if !mw.RedirectRegex.Permanent {
		t.Error("permanent was requested and dropped")
	}
}

// A forwarding rule must never take a hostname away from a project that
// actually serves it, whichever was configured first.
func TestProjectRoutesWinOverServerRedirects(t *testing.T) {
	set := settings()
	set.Redirects = []store.Redirect{{Domain: "app.example.com", Target: "elsewhere.example.com"}}

	d := Build(set, []Live{{
		Project:    project("app", "app.example.com"),
		Spec:       spec(t, "port: 8080\n"),
		Deployment: "dep1",
	}})

	for name, r := range d.HTTP.Routers {
		if strings.Contains(r.Rule, "`app.example.com`") && len(r.Middlewares) > 0 {
			t.Fatalf("router %s redirects a hostname the project serves", name)
		}
	}
	if _, ok := d.HTTP.Middlewares["publix-fw-0-redirect"]; ok {
		t.Error("the forwarding middleware was emitted anyway")
	}
}

func TestRedirectValidation(t *testing.T) {
	set := &store.Settings{Redirects: []store.Redirect{{Domain: "taken.example.com", Target: "a.example.com"}}}

	bad := map[string]store.Redirect{
		"no domain":     {Target: "a.example.com"},
		"no target":     {Domain: "b.example.com"},
		"not a host":    {Domain: "not a host", Target: "a.example.com"},
		"self loop":     {Domain: "a.example.com", Target: "a.example.com"},
		"duplicate":     {Domain: "taken.example.com", Target: "b.example.com"},
		"relative path": {Domain: "c.example.com", Path: "docs", Target: "a.example.com"},
	}
	for name, r := range bad {
		if err := set.ValidateRedirect(r, ""); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := set.ValidateRedirect(store.Redirect{Domain: "ok.example.com", Target: "https://a.example.com/x"}, ""); err != nil {
		t.Errorf("a valid rule was rejected: %v", err)
	}
	// Editing a rule in place must not collide with itself.
	if err := set.ValidateRedirect(store.Redirect{Domain: "taken.example.com", Target: "c.example.com"}, "taken.example.com"); err != nil {
		t.Errorf("editing a rule in place was rejected: %v", err)
	}
}

// A file whose http section has no content makes Traefik discard the whole
// file-provider directory, which on a real server took down the
// hand-written router serving publix's own dashboard. A server with nothing
// deployed must leave no file at all.
func TestWriteRemovesTheFileWhenNothingIsRouted(t *testing.T) {
	dir := t.TempDir()
	set := settings()
	set.TraefikDynamicDir = dir
	path := Path(set)

	// Something live first, so there is a file to remove.
	if err := Write(set, Build(set, []Live{{
		Project: project("api", "api.example.com"), Spec: spec(t, "port: 8080\n"), Deployment: "d1",
	}})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("nothing was written for a live project: %v", err)
	}

	// Now nothing is live.
	if err := Write(set, Build(set, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(path)
		t.Fatalf("the file survived with nothing to route:\n%s", raw)
	}

	// Removing it again is not an error: reconcile runs on every deploy.
	if err := Write(set, Build(set, nil)); err != nil {
		t.Fatalf("removing an absent file failed: %v", err)
	}
}

// Whatever Write does with an empty configuration, it must never leave a
// file behind that says nothing — that is the shape Traefik chokes on.
func TestRenderedFileAlwaysCarriesContent(t *testing.T) {
	dir := t.TempDir()
	set := settings()
	set.TraefikDynamicDir = dir

	if err := Write(set, Build(set, nil)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(set))
	if os.IsNotExist(err) {
		return // No file at all is the correct outcome.
	}
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "http: {}") {
		t.Errorf("wrote an empty http section, which disables Traefik's whole file provider:\n%s", raw)
	}
}

func proxyRouterFor(t *testing.T, d *Dynamic, host string) (*Router, *Service) {
	t.Helper()
	for _, r := range d.HTTP.Routers {
		if strings.Contains(r.Rule, "`"+host+"`") {
			return r, d.HTTP.Services[r.Service]
		}
	}
	t.Fatalf("no router for %s; routers were %v", host, d.HTTP.Routers)
	return nil, nil
}

// The whole point of a proxy rather than a redirect: the request reaches
// the backend, so the visitor's address bar keeps the hostname they typed.
func TestProxyServesAnExternalBackend(t *testing.T) {
	set := settings()
	set.Proxies = []store.Proxy{{Domain: "nas.example.com", Target: "http://10.0.0.5:8080"}}

	r, svc := proxyRouterFor(t, Build(set, nil), "nas.example.com")
	if len(r.Middlewares) != 0 {
		t.Errorf("a plain proxy needs no middleware, got %v", r.Middlewares)
	}
	if svc == nil || svc.LoadBalancer == nil || len(svc.LoadBalancer.Servers) != 1 {
		t.Fatalf("no backend behind the router: %+v", svc)
	}
	if got := svc.LoadBalancer.Servers[0].URL; got != "http://10.0.0.5:8080" {
		t.Errorf("backend = %q", got)
	}
	if svc.LoadBalancer.PassHostHeader != nil {
		t.Error("passHostHeader was emitted even though the default is what we want")
	}
	if r.TLS == nil {
		t.Error("no TLS, so the hostname would have no certificate")
	}
}

// A bare host:port is what people type; it has to mean http, not nothing.
func TestProxyTargetGainsAScheme(t *testing.T) {
	set := settings()
	set.Proxies = []store.Proxy{{Domain: "app.example.com", Target: "10.0.0.5:3000"}}

	_, svc := proxyRouterFor(t, Build(set, nil), "app.example.com")
	if got := svc.LoadBalancer.Servers[0].URL; got != "http://10.0.0.5:3000" {
		t.Errorf("backend = %q, want an http:// URL", got)
	}
}

func TestProxyOptionsReachTraefik(t *testing.T) {
	pass := false
	set := settings()
	set.Proxies = []store.Proxy{{
		Domain: "app.example.com", Path: "/api", Target: "https://10.0.0.5:8443",
		StripPath: true, PassHostHeader: &pass, InsecureSkipVerify: true,
	}}

	d := Build(set, nil)
	r, svc := proxyRouterFor(t, d, "app.example.com")

	if !strings.Contains(r.Rule, "PathPrefix(`/api`)") {
		t.Errorf("rule = %q, want the path prefix", r.Rule)
	}
	if len(r.Middlewares) != 1 {
		t.Fatalf("stripPath was requested but no middleware attached: %v", r.Middlewares)
	}
	mw := d.HTTP.Middlewares[r.Middlewares[0]]
	if mw.StripPrefix == nil || len(mw.StripPrefix.Prefixes) != 1 || mw.StripPrefix.Prefixes[0] != "/api" {
		t.Errorf("middleware strips %+v, want /api", mw.StripPrefix)
	}
	if svc.LoadBalancer.PassHostHeader == nil || *svc.LoadBalancer.PassHostHeader {
		t.Error("passHostHeader off was requested and lost — a pointer is what keeps false meaningful")
	}
	transport := d.HTTP.ServersTransports[svc.LoadBalancer.ServersTransport]
	if transport == nil || !transport.InsecureSkipVerify {
		t.Errorf("insecureSkipVerify was requested but no transport carries it: %+v", d.HTTP.ServersTransports)
	}
}

// The same precedence rule as redirects: a hostname a project serves is
// never taken over.
func TestProjectRoutesWinOverProxies(t *testing.T) {
	set := settings()
	set.Proxies = []store.Proxy{{Domain: "app.example.com", Target: "http://10.0.0.5:8080"}}

	d := Build(set, []Live{{
		Project: project("app", "app.example.com"), Spec: spec(t, "port: 8080\n"), Deployment: "dep1",
	}})

	if _, ok := d.HTTP.Services["publix-px-0"]; ok {
		t.Error("the proxy backend was emitted for a hostname the project serves")
	}
	r, _ := proxyRouterFor(t, d, "app.example.com")
	if !strings.Contains(r.Service, "@docker") {
		t.Errorf("the hostname routes to %q, not the project's deployment", r.Service)
	}
}

func TestProxyValidation(t *testing.T) {
	set := &store.Settings{
		Proxies:   []store.Proxy{{Domain: "taken.example.com", Target: "http://10.0.0.5:80"}},
		Redirects: []store.Redirect{{Domain: "moved.example.com", Target: "elsewhere.example.com"}},
	}

	bad := map[string]store.Proxy{
		"no domain":          {Target: "http://10.0.0.5:80"},
		"no target":          {Domain: "a.example.com"},
		"not a host":         {Domain: "not a host", Target: "http://10.0.0.5:80"},
		"self loop":          {Domain: "a.example.com", Target: "http://a.example.com:80"},
		"duplicate":          {Domain: "taken.example.com", Target: "http://10.0.0.6:80"},
		"relative path":      {Domain: "c.example.com", Path: "api", Target: "http://10.0.0.5:80"},
		"strip nothing":      {Domain: "d.example.com", Target: "http://10.0.0.5:80", StripPath: true},
		"already a redirect": {Domain: "moved.example.com", Target: "http://10.0.0.5:80"},
	}
	for name, p := range bad {
		if err := set.ValidateProxy(p, ""); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := set.ValidateProxy(store.Proxy{Domain: "ok.example.com", Target: "10.0.0.5:3000"}, ""); err != nil {
		t.Errorf("a valid proxy was rejected: %v", err)
	}
	if err := set.ValidateProxy(store.Proxy{Domain: "taken.example.com", Target: "http://10.0.0.9:80"}, "taken.example.com"); err != nil {
		t.Errorf("editing a proxy in place was rejected: %v", err)
	}
}

// One hostname cannot both proxy and redirect; whichever Traefik picked
// would be arbitrary.
func TestRedirectRefusesAProxiedHostname(t *testing.T) {
	set := &store.Settings{Proxies: []store.Proxy{{Domain: "a.example.com", Target: "http://10.0.0.5:80"}}}
	if err := set.ValidateRedirect(store.Redirect{Domain: "a.example.com", Target: "b.example.com"}, ""); err == nil {
		t.Error("a redirect was accepted for a hostname that is already proxied")
	}
}

// A project route may write redirectTo with or without a scheme. Prefixing
// one blindly produced https://https://host, and every visitor to that
// domain landed on a hostname that does not resolve.
func TestRedirectToKeepsAnExplicitScheme(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"new.example.com", "https://new.example.com/${1}"},
		{"https://new.example.com", "https://new.example.com/${1}"},
		{"http://new.example.com", "http://new.example.com/${1}"},
		{"https://new.example.com/", "https://new.example.com/${1}"},
	} {
		d := Build(settings(), []Live{{
			Project:    project("api"),
			Spec:       spec(t, "port: 8080\nroutes:\n  - domain: old.example.com\n    redirectTo: "+tc.in+"\n"),
			Deployment: "dep1",
		}})
		var mw *Middleware
		for _, r := range d.HTTP.Routers {
			if strings.Contains(r.Rule, "old.example.com") && len(r.Middlewares) == 1 {
				mw = d.HTTP.Middlewares[r.Middlewares[0]]
			}
		}
		if mw == nil || mw.RedirectRegex == nil {
			t.Fatalf("%q: no redirect middleware", tc.in)
		}
		if got := mw.RedirectRegex.Replacement; got != tc.want {
			t.Errorf("redirectTo %q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}
