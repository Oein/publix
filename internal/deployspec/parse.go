package deployspec

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/compose"
	"github.com/Oein/publix/internal/framework"
	"gopkg.in/yaml.v3"
)

// Find locates deployment.yaml inside a checkout.
func Find(root string) (string, bool) {
	for _, name := range Filenames {
		p := filepath.Join(root, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

// Load reads deployment.yaml from a checkout. A repository without one is
// not an error: publix falls back to detection, which is what makes
// one-click import from GitHub work on repositories that know nothing
// about publix.
func Load(root string) (*Spec, error) {
	path, ok := Find(root)
	if !ok {
		return &Spec{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sp, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return sp, nil
}

// Parse decodes deployment.yaml without resolving it against a checkout.
func Parse(raw []byte) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if strings.Contains(err.Error(), "EOF") {
			return &Spec{}, nil // an empty file means "use every default"
		}
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return &s, nil
}

// ComposeFilenames are the conventional compose file names.
var ComposeFilenames = []string{
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
}

// Resolve fills in everything the spec left unsaid by inspecting the
// checkout at root, then validates the result.
//
// This is where "one file, and a short one" becomes possible: the spec
// carries only what the repository cannot tell us about itself.
func (s *Spec) Resolve(root string) (*Resolved, error) {
	return s.ResolveFrom(root, framework.Dir(root))
}

// ResolveFrom resolves against an explicit source, so the same code path
// serves a local checkout and a repository inspected over the GitHub API.
func (s *Spec) ResolveFrom(root string, src framework.Source) (*Resolved, error) {
	out := *s // work on a copy; the caller's spec stays as written
	det := DetectFrom(src)

	if out.Kind == "" || out.Kind == KindAuto {
		// An explicit field in deployment.yaml is a decision; detection is
		// only a guess, so the field wins.
		switch {
		case out.Image != "":
			out.Kind = KindImage
		case out.Compose != "":
			out.Kind = KindCompose
		case out.Dockerfile != "":
			out.Kind = KindDockerfile
		case out.Build.Output != "" && out.Framework == "":
			out.Kind = KindStatic
		default:
			out.Kind = det.Kind
		}
	}
	if out.Context == "" {
		out.Context = "."
	}

	switch out.Kind {
	case KindCompose:
		if out.Compose == "" {
			out.Compose = det.Compose
		}
		if out.Compose == "" {
			for _, n := range ComposeFilenames {
				if src.Exists(n) {
					out.Compose = n
					break
				}
			}
		}
		if out.Service == "" {
			out.Service = det.Service
		}
		if out.Port == 0 {
			out.Port = det.Port
		}

	case KindDockerfile:
		if out.Dockerfile == "" {
			out.Dockerfile = firstNonEmpty(det.Dockerfile, "Dockerfile")
		}
		if out.Port == 0 {
			out.Port = det.Port
		}

	case KindFramework, KindStatic:
		// Everything here comes from the framework template unless the
		// spec overrode it, so a Next.js repository needs no build block
		// at all and a fussy one can still say exactly what it wants.
		if out.Framework == "" {
			out.Framework = det.Framework
		}
		fill(&out.Build.Install, det.Install)
		fill(&out.Build.Command, det.Command)
		fill(&out.Build.Start, det.Start)
		fill(&out.Build.Output, det.Output)
		if out.Build.SPA == nil && det.SPA {
			spa := true
			out.Build.SPA = &spa
		}
		if out.Port == 0 {
			out.Port = det.Port
		}
		if out.Kind == KindStatic && out.Port == 0 {
			out.Port = 80
		}

	case KindImage:
		if out.Port == 0 {
			out.Port = det.Port
		}
	}

	out.applyDefaults()

	r := &Resolved{Spec: &out, Detection: det, Root: root}
	if err := r.validate(src); err != nil {
		return nil, err
	}
	return r, nil
}

// fill sets dst from src only when dst is still empty.
func fill(dst *string, src string) {
	if *dst == "" {
		*dst = src
	}
}

// Resolved is a spec with every default filled in and validated against a
// specific checkout.
type Resolved struct {
	*Spec
	Detection Detection
	Root      string
}

func boolp(b bool) *bool { return &b }

// Bool dereferences an optional bool, falling back to def.
func Bool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (s *Spec) applyDefaults() {
	// `domains:` is the short form; `routes:` is the long one. Normalise
	// onto routes so downstream code has a single shape to handle.
	for _, d := range s.Domains {
		found := false
		for _, r := range s.Routes {
			if strings.EqualFold(r.Domain, d) {
				found = true
			}
		}
		if !found {
			s.Routes = append(s.Routes, Route{Domain: d})
		}
	}

	if s.Replicas == nil {
		one := 1
		s.Replicas = &one
	}
	if s.Health.Type == "" {
		switch {
		case len(s.Health.Command) > 0:
			s.Health.Type = HealthExec
		case s.Health.Path != "":
			s.Health.Type = HealthHTTP
		case s.Port > 0:
			s.Health.Type = HealthTCP
		default:
			s.Health.Type = HealthNone
		}
	}
	if s.Health.Type == HealthHTTP && s.Health.Path == "" {
		s.Health.Path = "/"
	}
	if s.Health.Status == 0 {
		s.Health.Status = 200
	}
	if s.Health.Port == 0 {
		s.Health.Port = s.Port
	}
	if s.Health.Interval == 0 {
		s.Health.Interval = Duration(2 * time.Second)
	}
	if s.Health.Timeout == 0 {
		s.Health.Timeout = Duration(5 * time.Second)
	}
	if s.Health.Grace == 0 {
		s.Health.Grace = Duration(90 * time.Second)
	}

	if s.Release.Strategy == "" {
		// Compose owns its own container names and host port bindings, so
		// two generations of a stack cannot coexist. Recreate is not a
		// preference there, it is the only correct answer.
		if s.Kind == KindCompose {
			s.Release.Strategy = StrategyRecreate
		} else {
			s.Release.Strategy = StrategyBlueGreen
		}
	}
	if s.Release.Drain == 0 {
		s.Release.Drain = Duration(10 * time.Second)
	}
	if s.Release.AutoRollback == nil {
		s.Release.AutoRollback = boolp(true)
	}
	if s.Release.Branch == "" {
		s.Release.Branch = "main"
	}

	for i := range s.Volumes {
		if s.Volumes[i].MountPath == "" {
			s.Volumes[i].MountPath = "/shared/" + s.Volumes[i].Name
		}
	}
}

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
var domainRe = regexp.MustCompile(`^([a-zA-Z0-9_]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)
var volNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// validate reports every problem at once. A user fixing their deployment.yaml
// should see the whole list, not discover one more on each retry.
func (r *Resolved) validate(src framework.Source) error {
	s := r.Spec
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if s.Name != "" && !nameRe.MatchString(s.Name) {
		add("name: %q must be lowercase alphanumeric with dashes (1-40 chars)", s.Name)
	}

	switch s.Kind {
	case KindDockerfile:
		rel := path.Join(s.Context, s.Dockerfile)
		if !src.Exists(rel) {
			add("dockerfile: %q does not exist in the repository", rel)
		}
		if s.Port <= 0 {
			add("port: is required — publix could not infer it, so add the port your app listens on")
		}

	case KindFramework:
		// A generated Dockerfile needs a start command and a port. Both
		// are normally detected; reaching here without them means
		// detection could not tell, and guessing would fail deep inside a
		// build instead of here.
		if s.Build.Start == "" {
			add("build.start: is required — publix could not work out how to run this project%s", detectionHint(r.Detection))
		}
		if s.Port <= 0 {
			add("port: is required — the port your app listens on")
		}
	case KindCompose:
		if s.Compose == "" {
			add("compose: no compose file found; name one explicitly")
			break
		}
		if !src.Exists(s.Compose) {
			add("compose: %q does not exist in the repository", s.Compose)
			break
		}
		raw, err := src.Read(s.Compose)
		if err != nil {
			add("compose: %v", err)
			break
		}
		f, err := compose.ParseBytes(raw, s.Compose)
		if err != nil {
			add("compose: %v", err)
			break
		}
		if s.Service == "" {
			add("service: is required — name which of [%s] receives the project's domains",
				strings.Join(f.ServiceNames(), ", "))
		} else if _, ok := f.Services[s.Service]; !ok {
			add("service: %q is not in %s (it has [%s])", s.Service, s.Compose, strings.Join(f.ServiceNames(), ", "))
		}
		if s.Port <= 0 && len(s.Routes) > 0 {
			add("port: is required — the port %q listens on inside its container", s.Service)
		}
		for i, rt := range s.Routes {
			if rt.Service != "" {
				if _, ok := f.Services[rt.Service]; !ok {
					add("routes[%d].service: %q is not in %s", i, rt.Service, s.Compose)
				}
			}
		}
	case KindStatic:
		if s.Build.Output == "" {
			add("build.output: is required for a static site (the directory your build writes, e.g. \"dist\")%s", detectionHint(r.Detection))
		} else if strings.HasPrefix(s.Build.Output, "/") || strings.Contains(s.Build.Output, "..") {
			add("build.output: %q must be a relative path inside the repository", s.Build.Output)
		}
	case KindImage:
		if s.Image == "" {
			add("image: is required for type: image")
		}
		if s.Port <= 0 {
			add("port: is required for type: image")
		}
	default:
		add("type: %q is not one of auto, dockerfile, compose, framework, static, image", s.Kind)
	}

	if s.Port < 0 || s.Port > 65535 {
		add("port: %d must be between 1 and 65535", s.Port)
	}
	if n := s.ReplicaCount(); n < 1 || n > 32 {
		add("replicas: %d must be between 1 and 32", n)
	}
	if s.ReplicaCount() > 1 && s.Kind == KindCompose {
		add("replicas: is not supported for compose projects — set `deploy.replicas` in the compose file instead")
	}

	seen := map[string]bool{}
	for i, rt := range s.Routes {
		switch {
		case rt.Domain == "":
			add("routes[%d].domain: is required", i)
			continue
		case !domainRe.MatchString(rt.Domain):
			add("routes[%d].domain: %q is not a valid hostname", i, rt.Domain)
		}
		key := strings.ToLower(rt.Domain) + "|" + rt.Path
		if seen[key] {
			add("routes[%d]: %s%s is declared twice", i, rt.Domain, rt.Path)
		}
		seen[key] = true
		if rt.Path != "" && !strings.HasPrefix(rt.Path, "/") {
			add("routes[%d].path: %q must start with /", i, rt.Path)
		}
		if rt.RedirectTo != "" && rt.Path != "" {
			add("routes[%d]: redirectTo cannot be combined with path", i)
		}
		for _, ba := range rt.BasicAuth {
			if !strings.Contains(ba, ":") {
				add("routes[%d].basicAuth: %q must be \"user:htpasswd-hash\"", i, ba)
			}
		}
	}

	volSeen := map[string]bool{}
	mountSeen := map[string]bool{}
	for i, v := range s.Volumes {
		if v.Name == "" {
			add("volumes[%d].name: is required", i)
			continue
		}
		if !volNameRe.MatchString(v.Name) {
			add("volumes[%d].name: %q must be lowercase alphanumeric with dots, dashes or underscores", i, v.Name)
		}
		if volSeen[v.Name] {
			add("volumes[%d]: %q is attached twice", i, v.Name)
		}
		volSeen[v.Name] = true

		m := v.Mount()
		if !strings.HasPrefix(m, "/") {
			add("volumes[%d].mountPath: %q must be an absolute path", i, m)
		}
		if mountSeen[m] {
			add("volumes[%d].mountPath: %q is used by more than one volume", i, m)
		}
		mountSeen[m] = true
		// A subPath escaping the project's own directory would reach
		// another project's data on the same shared volume.
		if v.SubPath != "" && (strings.HasPrefix(v.SubPath, "/") || containsDotDot(v.SubPath)) {
			add("volumes[%d].subPath: %q must be a relative path that stays inside the project's directory", i, v.SubPath)
		}
	}

	switch s.Health.Type {
	case HealthHTTP:
		if !strings.HasPrefix(s.Health.Path, "/") {
			add("health.path: %q must start with /", s.Health.Path)
		}
		if s.Health.Port <= 0 {
			add("health.port: is required when there is no top-level port")
		}
	case HealthExec:
		if len(s.Health.Command) == 0 {
			add("health.command: is required for health.type: exec")
		}
	case HealthTCP:
		if s.Health.Port <= 0 {
			add("health.port: is required for health.type: tcp")
		}
	case HealthNone:
	default:
		add("health.type: %q is not one of http, tcp, exec, none", s.Health.Type)
	}
	if s.Health.Type != HealthNone && s.Health.Grace.D() < s.Health.Interval.D() {
		add("health.grace: %s must be at least health.interval (%s)", s.Health.Grace.D(), s.Health.Interval.D())
	}

	switch s.Release.Strategy {
	case StrategyBlueGreen, StrategyRecreate:
	default:
		add("release.strategy: %q is not one of blue-green, recreate", s.Release.Strategy)
	}

	if s.Resources.CPU != "" {
		if f, err := strconv.ParseFloat(s.Resources.CPU, 64); err != nil || f <= 0 {
			add("resources.cpu: %q must be a positive number of cores, e.g. \"0.5\"", s.Resources.CPU)
		}
	}
	for field, val := range map[string]string{"memory": s.Resources.Memory, "memoryReservation": s.Resources.MemoryReservation} {
		if val == "" {
			continue
		}
		if _, err := ParseSize(val); err != nil {
			add("resources.%s: %v", field, err)
		}
	}

	cronSeen := map[string]bool{}
	for i, c := range s.Cron {
		if c.Name == "" {
			add("cron[%d].name: is required", i)
		} else if !nameRe.MatchString(c.Name) {
			add("cron[%d].name: %q must be lowercase alphanumeric with dashes", i, c.Name)
		} else if cronSeen[c.Name] {
			add("cron[%d].name: %q is declared twice", i, c.Name)
		}
		cronSeen[c.Name] = true
		if c.Schedule == "" {
			add("cron[%d].schedule: is required (5-field cron, e.g. \"0 3 * * *\")", i)
		}
		if len(c.Command) == 0 {
			add("cron[%d].command: is required", i)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Problems: errs}
}

func containsDotDot(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ValidationError carries every problem found in one validation pass.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "deployment.yaml has %d problem", len(e.Problems))
	if len(e.Problems) != 1 {
		b.WriteString("s")
	}
	b.WriteString(":\n")
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "  - %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ParseSize converts docker-style byte sizes ("512M", "2g") to bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	mult := int64(1)
	trimmed := s
	if n := len(trimmed); n > 0 {
		switch trimmed[n-1] {
		case 'b', 'B':
			if n >= 2 {
				switch trimmed[n-2] {
				case 'k', 'K':
					mult, trimmed = 1<<10, trimmed[:n-2]
				case 'm', 'M':
					mult, trimmed = 1<<20, trimmed[:n-2]
				case 'g', 'G':
					mult, trimmed = 1<<30, trimmed[:n-2]
				default:
					trimmed = trimmed[:n-1]
				}
			} else {
				trimmed = trimmed[:n-1]
			}
		case 'k', 'K':
			mult, trimmed = 1<<10, trimmed[:n-1]
		case 'm', 'M':
			mult, trimmed = 1<<20, trimmed[:n-1]
		case 'g', 'G':
			mult, trimmed = 1<<30, trimmed[:n-1]
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(trimmed), 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a valid size (use e.g. \"512M\" or \"2G\")", s)
	}
	return int64(n * float64(mult)), nil
}

// detectionHint appends what publix did work out, so an error that asks for
// a setting also says why it could not supply one itself.
func detectionHint(d Detection) string {
	if len(d.Notes) > 0 {
		return ".\n    " + d.Notes[0]
	}
	if d.Name != "" && d.Name != "Unknown" {
		return " (it looks like " + d.Name + ")"
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
