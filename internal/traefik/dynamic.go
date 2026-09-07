package traefik

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
	"gopkg.in/yaml.v3"
)

// DynamicFilename is the single file publix owns inside Traefik's file
// provider directory. Everything else there is left untouched, so publix
// can share a Traefik instance with hand-written configuration.
const DynamicFilename = "publix.yml"

// Dynamic is the Traefik dynamic configuration publix generates.
type Dynamic struct {
	HTTP HTTPConfig `yaml:"http"`
}

// HTTPConfig is the http section of a dynamic configuration.
type HTTPConfig struct {
	Routers     map[string]*Router     `yaml:"routers,omitempty"`
	Middlewares map[string]*Middleware `yaml:"middlewares,omitempty"`
	Services    map[string]*Service    `yaml:"services,omitempty"`
	// ServersTransports configure how Traefik reaches a backend. publix
	// emits one only for a proxy that has to accept an untrusted backend
	// certificate.
	ServersTransports map[string]*ServersTransport `yaml:"serversTransports,omitempty"`
}

// ServersTransport is how Traefik connects to a backend.
type ServersTransport struct {
	InsecureSkipVerify bool `yaml:"insecureSkipVerify,omitempty"`
}

// Router is one Traefik router.
type Router struct {
	Rule        string     `yaml:"rule"`
	EntryPoints []string   `yaml:"entryPoints,omitempty"`
	Service     string     `yaml:"service"`
	Middlewares []string   `yaml:"middlewares,omitempty"`
	Priority    int        `yaml:"priority,omitempty"`
	TLS         *RouterTLS `yaml:"tls,omitempty"`
}

// RouterTLS attaches certificate provisioning to a router.
type RouterTLS struct {
	CertResolver string `yaml:"certResolver,omitempty"`
}

// Middleware is a Traefik middleware. Only the kinds publix emits are
// modelled, and exactly one field is set on any instance.
type Middleware struct {
	StripPrefix   *StripPrefix   `yaml:"stripPrefix,omitempty"`
	RedirectRegex *RedirectRegex `yaml:"redirectRegex,omitempty"`
	BasicAuth     *BasicAuth     `yaml:"basicAuth,omitempty"`
	Headers       *Headers       `yaml:"headers,omitempty"`
}

// StripPrefix removes a path prefix before proxying.
type StripPrefix struct {
	Prefixes []string `yaml:"prefixes"`
}

// RedirectRegex issues an HTTP redirect instead of proxying.
type RedirectRegex struct {
	Regex       string `yaml:"regex"`
	Replacement string `yaml:"replacement"`
	Permanent   bool   `yaml:"permanent"`
}

// BasicAuth guards a router with htpasswd credentials.
type BasicAuth struct {
	Users []string `yaml:"users"`
}

// Headers sets custom response headers.
type Headers struct {
	CustomResponseHeaders map[string]string `yaml:"customResponseHeaders,omitempty"`
}

// Service is an explicit load-balancer definition. publix normally
// references docker-provider services instead and only emits one of these
// as the unreachable backend a pure-redirect router still has to name.
type Service struct {
	LoadBalancer *LoadBalancer `yaml:"loadBalancer,omitempty"`
}

// LoadBalancer lists backend servers.
type LoadBalancer struct {
	Servers []Server `yaml:"servers"`
	// PassHostHeader is a pointer so "false" survives: Traefik's own
	// default is true, and omitting the field is not the same as setting
	// it off.
	PassHostHeader   *bool  `yaml:"passHostHeader,omitempty"`
	ServersTransport string `yaml:"serversTransport,omitempty"`
}

// Server is one backend address.
type Server struct {
	URL string `yaml:"url"`
}

// Live describes one project's currently intended routing.
type Live struct {
	Project *store.Project
	// Spec is the resolved deployment.yaml of the live deployment. Routes
	// come from here, which is why a rollback restores that commit's
	// domains along with its code.
	Spec *deployspec.Spec
	// Deployment is the deployment ID that should receive traffic. Empty
	// means the project currently has nothing live.
	Deployment string
}

// Build assembles the whole dynamic configuration from every project's
// intended routing. It is a pure function of its inputs, which is what lets
// the reconciler write the file idempotently and diff it before writing.
func Build(set *store.Settings, live []Live) *Dynamic {
	d := &Dynamic{HTTP: HTTPConfig{
		Routers:           map[string]*Router{},
		Middlewares:       map[string]*Middleware{},
		Services:          map[string]*Service{},
		ServersTransports: map[string]*ServersTransport{},
	}}

	sorted := append([]Live(nil), live...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Project.Slug < sorted[j].Project.Slug })

	// Projects first: a hostname a project actually serves is never taken
	// over by a forwarding rule, whatever order they were configured in.
	claimed := map[string]bool{}
	for _, l := range sorted {
		for _, r := range Hosts(set, l.Project, l.Spec) {
			claimed[routeKey(r.Domain, r.Path)] = true
		}
		buildProject(d, set, l)
	}

	buildProxies(d, set, claimed)
	buildRedirects(d, set, claimed)

	if len(d.HTTP.Middlewares) == 0 {
		d.HTTP.Middlewares = nil
	}
	if len(d.HTTP.Services) == 0 {
		d.HTTP.Services = nil
	}
	if len(d.HTTP.ServersTransports) == 0 {
		d.HTTP.ServersTransports = nil
	}
	return d
}

// Hosts returns every hostname a project should answer on: the ones it
// declares, the ones configured in the dashboard, and its generated
// <slug>.<appsDomain>.
func Hosts(set *store.Settings, p *store.Project, sp *deployspec.Spec) []deployspec.Route {
	var routes []deployspec.Route
	seen := map[string]bool{}

	add := func(r deployspec.Route) {
		key := strings.ToLower(r.Domain) + "|" + r.Path
		if r.Domain == "" || seen[key] {
			return
		}
		seen[key] = true
		routes = append(routes, r)
	}

	if sp != nil {
		for _, r := range sp.Routes {
			add(r)
		}
	}
	for _, dom := range p.Domains {
		add(deployspec.Route{Domain: dom})
	}
	if h := ProjectHost(p.Slug, set.AppsDomainFor(p)); h != "" {
		add(deployspec.Route{Domain: h})
	}
	return routes
}

func routeKey(domain, path string) string {
	return strings.ToLower(domain) + "|" + path
}

// buildProxies attaches hostnames to backends publix does not deploy.
//
// Unlike a redirect, this is a real reverse proxy: a router and a service
// pointing at an address, so the visitor's hostname is what stays in the
// address bar and the backend can be anywhere Traefik can reach.
func buildProxies(d *Dynamic, set *store.Settings, claimed map[string]bool) {
	sorted := append([]store.Proxy(nil), set.Proxies...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Domain != sorted[j].Domain {
			return sorted[i].Domain < sorted[j].Domain
		}
		return sorted[i].Path < sorted[j].Path
	})

	for i, p := range sorted {
		if claimed[routeKey(p.Domain, p.Path)] {
			continue
		}
		name := fmt.Sprintf("publix-px-%d", i)

		lb := &LoadBalancer{Servers: []Server{{URL: p.TargetURL()}}}
		if !p.PassesHost() {
			pass := false
			lb.PassHostHeader = &pass
		}
		if p.InsecureSkipVerify {
			transport := name + "-transport"
			d.HTTP.ServersTransports[transport] = &ServersTransport{InsecureSkipVerify: true}
			lb.ServersTransport = transport
		}
		d.HTTP.Services[name] = &Service{LoadBalancer: lb}

		router := &Router{
			Rule:        hostRule(p.Domain, p.Path),
			EntryPoints: set.EntryPoints,
			Service:     name,
			TLS:         routerTLS(set, deployspec.Route{Domain: p.Domain}),
		}
		if p.StripPath && p.Path != "" {
			mw := name + "-strip"
			d.HTTP.Middlewares[mw] = &Middleware{StripPrefix: &StripPrefix{Prefixes: []string{p.Path}}}
			router.Middlewares = []string{mw}
		}
		d.HTTP.Routers[name] = router
	}
}

// buildRedirects emits the server's own forwarding rules: hostnames with
// nothing deployed behind them.
//
// They need a service to reference even though no request reaches one, so
// they share the same dead-end backend the per-project redirects use.
func buildRedirects(d *Dynamic, set *store.Settings, claimed map[string]bool) {
	sorted := append([]store.Redirect(nil), set.Redirects...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Domain != sorted[j].Domain {
			return sorted[i].Domain < sorted[j].Domain
		}
		return sorted[i].Path < sorted[j].Path
	})

	for i, r := range sorted {
		if claimed[routeKey(r.Domain, r.Path)] {
			continue
		}
		// A hostname that is proxied is served, not redirected. Validation
		// refuses the combination, but a state file edited by hand could
		// still carry both.
		if _, proxied := set.Proxy(r.Domain, r.Path); proxied {
			continue
		}
		name := fmt.Sprintf("publix-fw-%d", i)
		d.HTTP.Middlewares[name+"-redirect"] = &Middleware{
			RedirectRegex: redirectRegex(r),
		}
		d.HTTP.Routers[name] = &Router{
			Rule:        hostRule(r.Domain, r.Path),
			EntryPoints: set.EntryPoints,
			Service:     noopService(d),
			Middlewares: []string{name + "-redirect"},
			TLS:         routerTLS(set, deployspec.Route{Domain: r.Domain}),
		}
	}
}

// redirectRegex turns a forwarding rule into Traefik's rewrite.
//
// Anchoring on the source host rather than matching everything keeps the
// middleware inert if it is ever attached to another router by mistake.
func redirectRegex(r store.Redirect) *RedirectRegex {
	source := `^https?://` + regexpQuote(r.Domain)
	if r.Keeps() {
		return &RedirectRegex{
			Regex:       source + `/(.*)`,
			Replacement: r.TargetURL() + "/${1}",
			Permanent:   r.Permanent,
		}
	}
	// Discarding the path is the deliberate choice to land everyone on one
	// page, so the replacement carries no capture group — and no trailing
	// slash either, which would turn a target of /landing into /landing/.
	return &RedirectRegex{
		Regex:       source + `/.*`,
		Replacement: r.TargetURL(),
		Permanent:   r.Permanent,
	}
}

func buildProject(d *Dynamic, set *store.Settings, l Live) {
	p := l.Project
	routes := Hosts(set, p, l.Spec)

	for i, route := range routes {
		name := fmt.Sprintf("publix-r-%s-%d", p.Slug, i)

		if route.RedirectTo != "" {
			// A redirect needs no backend, but Traefik still requires a
			// service reference. Point it at an address the middleware
			// never lets a request reach.
			d.HTTP.Routers[name] = &Router{
				Rule:        hostRule(route.Domain, ""),
				EntryPoints: set.EntryPoints,
				Service:     noopService(d),
				Middlewares: []string{addRedirect(d, name, route)},
				TLS:         routerTLS(set, route),
			}
			continue
		}

		// With nothing live, emit no router at all rather than one pointing
		// at a service that does not exist. Traefik answers 404 either way,
		// but an absent router keeps the generated file honest about what
		// is actually running.
		if l.Deployment == "" {
			continue
		}

		r := &Router{
			Rule:        hostRule(route.Domain, route.Path),
			EntryPoints: set.EntryPoints,
			Service:     ServiceName(p.Slug, l.Deployment) + "@docker",
			TLS:         routerTLS(set, route),
		}
		if route.Service != "" {
			// A compose stack can expose more than one service; a route may
			// name which one it reaches.
			r.Service = ServiceName(p.Slug+"-"+Slug(route.Service), l.Deployment) + "@docker"
		}
		if route.Path != "" && route.StripPath {
			r.Middlewares = append(r.Middlewares, addStrip(d, name, route.Path))
		}
		if len(route.Headers) > 0 {
			r.Middlewares = append(r.Middlewares, addHeaders(d, name, route.Headers))
		}
		if len(route.BasicAuth) > 0 {
			r.Middlewares = append(r.Middlewares, addBasicAuth(d, name, route.BasicAuth))
		}
		d.HTTP.Routers[name] = r
	}
}

func addStrip(d *Dynamic, base, prefix string) string {
	name := base + "-strip"
	d.HTTP.Middlewares[name] = &Middleware{StripPrefix: &StripPrefix{Prefixes: []string{prefix}}}
	return name
}

func addHeaders(d *Dynamic, base string, h map[string]string) string {
	name := base + "-headers"
	d.HTTP.Middlewares[name] = &Middleware{Headers: &Headers{CustomResponseHeaders: h}}
	return name
}

func addBasicAuth(d *Dynamic, base string, users []string) string {
	name := base + "-auth"
	d.HTTP.Middlewares[name] = &Middleware{BasicAuth: &BasicAuth{Users: users}}
	return name
}

// noopService registers the dead-end backend a redirect router points at.
// Traefik requires every router to name a service; the middleware answers
// before a request could ever reach this one.
func noopService(d *Dynamic) string {
	const name = "publix-noop"
	d.HTTP.Services[name] = &Service{
		LoadBalancer: &LoadBalancer{Servers: []Server{{URL: "http://127.0.0.1:1"}}},
	}
	return name
}

func addRedirect(d *Dynamic, base string, route deployspec.Route) string {
	name := base + "-redirect"
	d.HTTP.Middlewares[name] = &Middleware{RedirectRegex: &RedirectRegex{
		Regex:       `^https?://` + regexpQuote(route.Domain) + `/(.*)`,
		Replacement: "https://" + route.RedirectTo + "/${1}",
		Permanent:   true,
	}}
	return name
}

// hostRule builds a Traefik matcher for a host and optional path prefix.
func hostRule(host, path string) string {
	rule := "Host(`" + host + "`)"
	if path != "" && path != "/" {
		rule += " && PathPrefix(`" + path + "`)"
	}
	return rule
}

func routerTLS(set *store.Settings, route deployspec.Route) *RouterTLS {
	if !set.TLSEnabled() || !deployspec.Bool(route.TLS, true) {
		return nil
	}
	return &RouterTLS{CertResolver: set.CertResolver}
}

// regexpQuote escapes the characters that occur in hostnames and are
// meaningful to Go's regexp syntax.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.+*?()|[]{}^$\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Render serialises the configuration with a header warning humans off it.
func (d *Dynamic) Render() ([]byte, error) {
	raw, err := yaml.Marshal(d)
	if err != nil {
		return nil, err
	}
	header := "# Managed by publix. Do not edit.\n" +
		"#\n" +
		"# This file is regenerated on every deploy, promote and rollback.\n" +
		"# Production hostnames live here rather than on the containers so\n" +
		"# that moving traffic between deployments is a file rewrite Traefik\n" +
		"# hot-reloads, not a container restart.\n"
	return append([]byte(header), raw...), nil
}

// Path is the full location of publix's dynamic configuration file.
func Path(set *store.Settings) string {
	return filepath.Join(set.TraefikDynamicDir, DynamicFilename)
}

// Empty reports whether this configuration routes nothing at all.
func (d *Dynamic) Empty() bool {
	return len(d.HTTP.Routers) == 0 && len(d.HTTP.Services) == 0 && len(d.HTTP.Middlewares) == 0
}

// Write renders the configuration into Traefik's file provider directory.
//
// The write is atomic and is skipped entirely when nothing changed, so one
// project's deploy never makes Traefik reload every route on the host.
//
// With nothing to route the file is removed rather than written empty. A
// file whose http section has no content makes Traefik discard the entire
// file-provider directory — including the hand-written router that serves
// publix's own dashboard, which sits beside this one. So a server with no
// live deployment would take its own dashboard offline, and the only clue
// would be a 404 on a hostname whose router is plainly there on disk.
func Write(set *store.Settings, d *Dynamic) error {
	path := Path(set)
	if err := os.MkdirAll(set.TraefikDynamicDir, 0o755); err != nil {
		return fmt.Errorf("cannot create the Traefik dynamic directory %s: %w\n\nPoint `traefikDynamicDir` at a directory publix can write to.", set.TraefikDynamicDir, err)
	}

	if d.Empty() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove %s: %w", path, err)
		}
		return nil
	}

	raw, err := d.Render()
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(raw) {
		return nil
	}
	return store.WriteFileAtomic(path, raw, 0o644)
}
