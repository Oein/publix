package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// projectView is a project as the dashboard shows it, enriched with the
// derived facts the UI would otherwise have to compute for itself.
type projectView struct {
	*store.Project
	URL   string   `json:"url,omitempty"`
	Hosts []string `json:"hosts"`
	// Live is the deployment currently serving traffic, if any.
	Live *store.Deployment `json:"live,omitempty"`
	// Latest is the most recent deployment whatever its outcome, so a
	// project whose only deployment failed does not read as never deployed.
	Latest   *store.Deployment `json:"latest,omitempty"`
	Building bool              `json:"building"`
	Kind     string            `json:"kind,omitempty"`
	// Framework is the detected stack, e.g. "nextjs". The dashboard shows
	// an icon for it, which is how a grid of projects becomes scannable.
	Framework string `json:"framework,omitempty"`
	// FrameworkName is the human label, e.g. "Next.js".
	FrameworkName string   `json:"frameworkName,omitempty"`
	Volumes       []string `json:"volumes,omitempty"`
	// GeneratedHost is the <slug>.<parent> address, separated from Hosts so
	// the dashboard can show which parent a project sits under and offer to
	// move it. Empty when the project opted out or none is registered.
	GeneratedHost string `json:"generatedHost,omitempty"`
	// AppsDomainResolved is the parent actually in use, which differs from
	// the stored choice when that choice was the server default.
	AppsDomainResolved string `json:"appsDomainResolved,omitempty"`
}

// resolveAppsDomain validates a project's choice of parent domain.
//
// The empty string means "the server default" and the sentinel means "no
// generated hostname"; anything else has to actually be registered, since
// a typo would otherwise produce a hostname nothing resolves to.
func (s *Server) resolveAppsDomain(want string) (string, error) {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" || want == store.AppsDomainNone {
		return want, nil
	}
	set := s.store.Settings()
	if !set.HasAppsDomain(want) {
		available := set.AppsDomainNames()
		if len(available) == 0 {
			return "", fmt.Errorf("no apps domain is registered on this server; add one under Settings → Domains")
		}
		return "", fmt.Errorf("%q is not a registered apps domain. Available: %s", want, strings.Join(available, ", "))
	}
	return want, nil
}

func (s *Server) view(p *store.Project) projectView {
	set := s.store.Settings()
	v := projectView{Project: p.Redacted(), Hosts: []string{}}

	if len(p.Deployments) > 0 {
		v.Latest = p.Deployments[0]
	}

	var sp *deployspec.Spec
	if live := p.LiveDeployment(); live != nil {
		v.Live = live
		v.Kind = live.Kind
		if parsed, err := parseSpec(live.Spec); err == nil {
			sp = parsed
			v.Framework = parsed.Framework
			for _, vol := range parsed.Volumes {
				v.Volumes = append(v.Volumes, vol.Name)
			}
		}
	}

	// What a project *is* does not depend on whether its last deploy
	// worked. Fall back to the most recent attempt so a project that has
	// never deployed successfully still shows what it is built with.
	if v.Framework == "" && v.Latest != nil {
		if v.Kind == "" {
			v.Kind = v.Latest.Kind
		}
		if parsed, err := parseSpec(v.Latest.Spec); err == nil {
			v.Framework = parsed.Framework
		}
	}
	// Fall back to the build kind so a Dockerfile or compose project still
	// gets a meaningful mark rather than a blank tile.
	if v.Framework == "" {
		v.Framework = v.Kind
	}
	v.FrameworkName = frameworkLabel(v.Framework)

	for _, r := range traefik.Hosts(&set, p, sp) {
		if r.RedirectTo == "" {
			v.Hosts = append(v.Hosts, r.Domain+r.Path)
		}
	}
	v.URL = engine.ProjectURL(&set, p, sp)
	v.AppsDomainResolved = set.AppsDomainFor(p)
	v.GeneratedHost = traefik.ProjectHost(p.Slug, v.AppsDomainResolved)
	_, v.Building = s.engine.Running(p.ID)
	return v
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects := s.store.Projects()
	out := make([]projectView, 0, len(projects))
	for _, p := range projects {
		out = append(out, s.view(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// project resolves the {id} path value, writing a 404 if it is unknown.
func (s *Server) project(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	p, ok := s.store.Project(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no project named %q", r.PathValue("id")))
		return nil, false
	}
	return p, true
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.view(p))
}

// createProjectBody is the payload for creating a project by hand, without
// going through GitHub import.
type createProjectBody struct {
	Name       string   `json:"name"`
	Repo       string   `json:"repo"`
	Branch     string   `json:"branch"`
	RootDir    string   `json:"rootDir"`
	SpecPath   string   `json:"specPath"`
	Domains    []string `json:"domains"`
	AutoDeploy bool     `json:"autoDeploy"`
	// AppsDomain picks which registered parent the generated hostname sits
	// under. Empty takes the server default.
	AppsDomain string `json:"appsDomain"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body createProjectBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("a project needs a name"))
		return
	}

	p := &store.Project{
		Name:       strings.TrimSpace(body.Name),
		RootDir:    body.RootDir,
		SpecPath:   body.SpecPath,
		Domains:    body.Domains,
		AutoDeploy: body.AutoDeploy,
	}
	if body.AppsDomain != "" {
		chosen, err := s.resolveAppsDomain(body.AppsDomain)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.AppsDomain = chosen
	}
	if body.Repo != "" {
		owner, name, ok := strings.Cut(body.Repo, "/")
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("repo must be in owner/name form, got %q", body.Repo))
			return
		}
		p.Repo = &store.Repo{
			Owner:    owner,
			Name:     name,
			Branch:   firstNonEmpty(body.Branch, "main"),
			CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", owner, name),
		}
	}

	created, err := s.store.CreateProject(p)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	// A new project has no live deployment, but it may already own domains,
	// and the routing file should reflect that immediately.
	if err := s.engine.ReconcileRouting(); err != nil {
		s.log.Warn("reconciling routing after project creation", "error", err)
	}
	writeJSON(w, http.StatusCreated, s.view(created))
}

// updateProjectBody carries only the fields the dashboard can change. Every
// field is a pointer so "not sent" is distinguishable from "set to empty".
type updateProjectBody struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Branch      *string   `json:"branch,omitempty"`
	RootDir     *string   `json:"rootDir,omitempty"`
	SpecPath    *string   `json:"specPath,omitempty"`
	AutoDeploy  *bool     `json:"autoDeploy,omitempty"`
	Paused      *bool     `json:"paused,omitempty"`
	Domains     *[]string `json:"domains,omitempty"`
	AppsDomain  *string   `json:"appsDomain,omitempty"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body updateProjectBody
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	updated, err := s.store.UpdateProject(p.ID, func(p *store.Project) error {
		if body.Name != nil {
			if strings.TrimSpace(*body.Name) == "" {
				return fmt.Errorf("a project needs a name")
			}
			p.Name = strings.TrimSpace(*body.Name)
		}
		if body.Description != nil {
			p.Description = *body.Description
		}
		if body.RootDir != nil {
			p.RootDir = strings.TrimPrefix(*body.RootDir, "/")
		}
		if body.SpecPath != nil {
			p.SpecPath = strings.TrimPrefix(*body.SpecPath, "/")
		}
		if body.AutoDeploy != nil {
			p.AutoDeploy = *body.AutoDeploy
		}
		if body.Paused != nil {
			p.Paused = *body.Paused
		}
		if body.Branch != nil && p.Repo != nil {
			p.Repo.Branch = *body.Branch
		}
		if body.Domains != nil {
			clean, err := normaliseDomains(*body.Domains)
			if err != nil {
				return err
			}
			p.Domains = clean
		}
		if body.AppsDomain != nil {
			chosen, err := s.resolveAppsDomain(*body.AppsDomain)
			if err != nil {
				return err
			}
			p.AppsDomain = chosen
		}
		return nil
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := s.engine.ReconcileRouting(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(updated))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	var warnings []string
	if err := s.engine.Teardown(ctx, p); err != nil {
		// A container that would not die must not prevent the project
		// record from being removed, or the dashboard becomes unusable.
		warnings = append(warnings, err.Error())
	}

	// Remove the webhook so the repository is not left calling a project
	// that no longer exists.
	if p.Repo != nil && p.Repo.HookID != 0 {
		if gh, err := s.github(); err == nil {
			if err := gh.DeleteHook(ctx, p.Repo.Owner, p.Repo.Name, p.Repo.HookID); err != nil {
				warnings = append(warnings, "could not remove the GitHub webhook: "+err.Error())
			}
		}
	}

	if err := s.store.DeleteProject(p.ID); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := s.engine.ReconcileRouting(); err != nil {
		warnings = append(warnings, err.Error())
	}

	set := s.store.Settings()
	var kept []string
	for _, v := range set.Volumes {
		// A shared volume's directory is not this project's to report as
		// retained: it was never only theirs.
		if !v.Shared() {
			kept = append(kept, fmt.Sprintf("%s (%s)", v.Name, v.Dir(p.ID)))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"warnings": warnings,
		// Deleting a project must never silently destroy the data it
		// accumulated; say plainly what was left behind.
		"retainedData": kept,
	})
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Ref   string `json:"ref,omitempty"`
		Force bool   `json:"force,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	dep, err := s.engine.Deploy(p.ID, engine.Options{
		Ref: body.Ref, Force: body.Force, Trigger: "manual",
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Deployment string `json:"deployment,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	dep, err := s.engine.Rollback(p.ID, body.Deployment)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

// handleRollbackPlan tells the user, before they commit to it, whether a
// rollback will be instant or will rebuild from source.
func (s *Server) handleRollbackPlan(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	plan, err := s.engine.PlanRollback(r.Context(), p.ID, r.URL.Query().Get("deployment"))
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	if err := s.engine.Cancel(p.ID, ""); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	dep, found := p.Deployment(r.PathValue("did"))
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("no deployment %q in this project", r.PathValue("did")))
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

// handleSetEnv replaces a project's environment variables.
//
// A secret whose value is sent back empty keeps the value already stored:
// the dashboard never receives secret values, so it cannot echo them back,
// and without this rule every save would blank them.
func (s *Server) handleSetEnv(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Env []store.EnvVar `json:"env"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cleaned, err := cleanEnv(body.Env)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// A secret the dashboard never received comes back empty, which means
	// "keep what you already have" rather than "clear it".
	existing := map[string]store.EnvVar{}
	for _, e := range p.Env {
		existing[e.Key] = e
	}
	for i, e := range cleaned {
		if e.Secret && e.Value == "" {
			if prev, had := existing[e.Key]; had {
				cleaned[i].Value = prev.Value
			}
		}
	}
	sort.Slice(cleaned, func(i, j int) bool { return cleaned[i].Key < cleaned[j].Key })

	updated, err := s.store.UpdateProject(p.ID, func(p *store.Project) error {
		p.Env = cleaned
		return nil
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(updated))
}

// cleanEnv validates and de-duplicates environment variables arriving from
// the dashboard. It is shared by the env screen and by import, where the
// same paste has to mean the same thing.
func cleanEnv(in []store.EnvVar) ([]store.EnvVar, error) {
	seen := map[string]bool{}
	out := make([]store.EnvVar, 0, len(in))
	for _, e := range in {
		key := strings.TrimSpace(e.Key)
		if key == "" {
			continue
		}
		if !validEnvKey(key) {
			return nil, fmt.Errorf("%q is not a valid environment variable name", key)
		}
		if seen[key] {
			return nil, fmt.Errorf("%q is listed twice", key)
		}
		seen[key] = true
		out = append(out, store.EnvVar{Key: key, Value: e.Value, Secret: e.Secret})
	}
	return out, nil
}

func validEnvKey(k string) bool {
	for i, r := range k {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return k != ""
}

func (s *Server) handleSetDomains(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Domains []string `json:"domains"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	clean, err := normaliseDomains(body.Domains)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Two projects claiming one hostname is a silent outage for whichever
	// loses; refuse it at the point the mistake is made.
	for _, other := range s.store.Projects() {
		if other.ID == p.ID {
			continue
		}
		for _, d := range other.Domains {
			for _, want := range clean {
				if strings.EqualFold(d, want) {
					writeError(w, http.StatusConflict, fmt.Errorf("%s is already used by the project %q", want, other.Name))
					return
				}
			}
		}
	}

	updated, err := s.store.UpdateProject(p.ID, func(p *store.Project) error {
		p.Domains = clean
		return nil
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if err := s.engine.ReconcileRouting(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(updated))
}

func normaliseDomains(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
		d = strings.TrimSuffix(d, "/")
		if d == "" {
			continue
		}
		if strings.ContainsAny(d, "/ \t:") {
			return nil, fmt.Errorf("%q is not a bare hostname", d)
		}
		if !strings.Contains(d, ".") {
			return nil, fmt.Errorf("%q does not look like a hostname", d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

// containerView is one running container as the dashboard shows it.
type containerView struct {
	Name       string  `json:"name"`
	ID         string  `json:"id"`
	State      string  `json:"state"`
	Status     string  `json:"status"`
	Deployment string  `json:"deployment"`
	Image      string  `json:"image"`
	Service    string  `json:"service,omitempty"`
	CPU        float64 `json:"cpu"`
	Memory     int64   `json:"memory"`
	MemLimit   int64   `json:"memoryLimit"`
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	containers, err := s.docker.ListContainers(ctx, true, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// A compose stack's containers carry compose's labels rather than a
	// per-replica publix label, so pick them up by compose project too.
	if extra, err := s.docker.ListContainers(ctx, true, "com.docker.compose.project="+traefik.ComposeProject(p.Slug)); err == nil {
		seen := map[string]bool{}
		for _, c := range containers {
			seen[c.ID] = true
		}
		for _, c := range extra {
			if !seen[c.ID] {
				containers = append(containers, c)
			}
		}
	}

	out := make([]containerView, 0, len(containers))
	for _, c := range containers {
		v := containerView{
			Name:       c.Name(),
			ID:         c.ID[:min(12, len(c.ID))],
			State:      c.State,
			Status:     c.Status,
			Deployment: c.Labels[traefik.LabelDeployment],
			Image:      c.Image,
			Service:    c.Labels["com.docker.compose.service"],
		}
		if c.State == "running" {
			if st, err := s.docker.ContainerStats(ctx, c.ID); err == nil {
				v.CPU, v.Memory, v.MemLimit = st.CPUPercent, st.MemUsage, st.MemLimit
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRunCron(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	job := r.PathValue("job")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if err := s.engine.RunCron(ctx, p.ID, job); err != nil {
			s.log.Warn("scheduled job failed", "project", p.Name, "job", job, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseSpec decodes a stored spec snapshot.
func parseSpec(raw string) (*deployspec.Spec, error) {
	if raw == "" {
		return nil, errNoSpec
	}
	return deployspec.Parse([]byte(raw))
}

var errNoSpec = errors.New("no spec recorded")

// frameworkLabels are the human names shown beside a project's icon.
var frameworkLabels = map[string]string{
	"nextjs": "Next.js", "nuxt": "Nuxt", "sveltekit": "SvelteKit",
	"remix": "Remix", "astro": "Astro", "nestjs": "NestJS",
	"gatsby": "Gatsby", "docusaurus": "Docusaurus", "angular": "Angular",
	"cra": "React", "vite": "Vite", "node": "Node.js",
	"go": "Go", "python": "Python", "django": "Django",
	"fastapi": "FastAPI", "flask": "Flask",
	"compose": "Docker Compose", "dockerfile": "Dockerfile",
	"static": "Static site", "framework": "Auto-detected", "image": "Prebuilt image",
}

func frameworkLabel(id string) string {
	if name, ok := frameworkLabels[id]; ok {
		return name
	}
	return id
}
