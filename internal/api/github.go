package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/framework"
	"github.com/Oein/publix/internal/github"
	"github.com/Oein/publix/internal/store"
	"gopkg.in/yaml.v3"
)

// handleGitHubStatus reports whether GitHub is connected and as whom.
func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	set := s.store.Settings()
	out := map[string]any{
		"configured":   set.GitHub.Configured(),
		"mode":         githubMode(set.GitHub),
		"apiBase":      firstNonEmpty(set.GitHub.APIBase, github.DefaultAPIBase),
		"webhookUrl":   s.webhookURL(r),
		"publicUrlSet": set.PublicURL != "",
		// The secret is shown deliberately. A GitHub App's webhook is
		// configured on the App, not on each repository, so the operator
		// has to paste this into GitHub by hand — and cannot if publix
		// keeps it to itself. This endpoint already requires a session.
		"webhookSecret": set.GitHub.WebhookSecret,
	}
	if !set.GitHub.Configured() {
		writeJSON(w, http.StatusOK, out)
		return
	}

	gh, err := s.github()
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if viewer, err := gh.Whoami(ctx); err != nil {
		out["error"] = err.Error()
	} else {
		out["login"] = viewer.Login
		out["avatar"] = viewer.AvatarURL
		out["type"] = viewer.Type
	}

	// In App mode, report where the App itself sends webhooks. If that
	// already points here, publix must not also create per-repository
	// hooks, or every push would arrive twice.
	if app, isApp, err := gh.App(ctx); isApp && err == nil {
		out["appName"] = app.Name
		out["appUrl"] = app.HTMLURL
		out["appWebhookUrl"] = app.HookAttributes.URL
		out["appWebhookActive"] = app.HookAttributes.Active
		out["appDeliversWebhooks"] = appDeliversWebhooks(app, s.webhookURL(r))
	} else if isApp && err != nil {
		out["appError"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// appDeliversWebhooks reports whether the App's own webhook already reaches
// this publix, which is what decides between App-level and per-repository
// delivery.
func appDeliversWebhooks(app *github.AppInfo, want string) bool {
	if app == nil || !app.HookAttributes.Active {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(app.HookAttributes.URL, "/"), strings.TrimSuffix(want, "/"))
}

func githubMode(g store.GitHubSettings) string {
	switch {
	case g.AppID != "":
		return "app"
	case g.Token != "":
		return "token"
	default:
		return "none"
	}
}

// webhookURL is the address GitHub should call. It is derived from the
// configured public URL, falling back to the request's own host so the
// settings page can show something useful before anything is configured.
func (s *Server) webhookURL(r *http.Request) string {
	base := strings.TrimSuffix(s.store.Settings().PublicURL, "/")
	if base == "" {
		scheme := "http"
		if isHTTPS(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/api/webhooks/github"
}

// handleSetGitHub stores GitHub credentials, verifying them before saving so
// a typo is caught at the point it is made rather than at the first deploy.
func (s *Server) handleSetGitHub(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token          string `json:"token"`
		AppID          string `json:"appId"`
		InstallationID string `json:"installationId"`
		PrivateKey     string `json:"privateKey"`
		APIBase        string `json:"apiBase"`
		// WebhookSecret lets an operator match a secret their App already
		// uses, rather than having to change it on GitHub's side.
		WebhookSecret string `json:"webhookSecret"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	current := s.store.Settings().GitHub
	candidate := store.GitHubSettings{
		Token:          strings.TrimSpace(body.Token),
		AppID:          strings.TrimSpace(body.AppID),
		InstallationID: strings.TrimSpace(body.InstallationID),
		PrivateKey:     strings.TrimSpace(body.PrivateKey),
		APIBase:        strings.TrimSpace(body.APIBase),
		WebhookSecret:  firstNonEmpty(strings.TrimSpace(body.WebhookSecret), current.WebhookSecret),
	}
	// The dashboard never receives the stored token or key back, so an
	// empty field on save means "leave it alone", not "clear it".
	if candidate.Token == "" && candidate.AppID == "" {
		candidate.Token = current.Token
	}
	if candidate.AppID != "" && candidate.PrivateKey == "" {
		candidate.PrivateKey = current.PrivateKey
	}
	if candidate.WebhookSecret == "" {
		candidate.WebhookSecret = store.NewToken()
	}

	client, err := github.New(candidate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	viewer, err := client.Whoami(ctx)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	candidate.Login = viewer.Login

	if err := s.store.SetSettings(func(set *store.Settings) error {
		set.GitHub = candidate
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.handleGitHubStatus(w, r)
}

// handleDisconnectGitHub clears the stored credentials.
func (s *Server) handleDisconnectGitHub(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetSettings(func(set *store.Settings) error {
		// The webhook secret is kept: existing hooks on repositories still
		// sign with it, and regenerating it would break them silently.
		secret := set.GitHub.WebhookSecret
		set.GitHub = store.GitHubSettings{WebhookSecret: secret}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.mu.Lock()
	s.gh, s.ghFingerp = nil, ""
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// appDelivers reports whether the configured GitHub App already sends this
// publix its webhooks, in which case per-repository hooks are redundant.
//
// It fails closed: if the App cannot be read, publix creates the repository
// hook. A duplicate delivery is noisy; no delivery at all means pushes
// silently stop deploying, which is worse.
func (s *Server) appDelivers(ctx context.Context, gh *github.Client, r *http.Request) bool {
	app, isApp, err := gh.App(ctx)
	if !isApp || err != nil {
		return false
	}
	return appDeliversWebhooks(app, s.webhookURL(r))
}

// repoView is a repository row on the import screen, annotated with whether
// it has already been imported.
type repoView struct {
	github.Repo
	Imported  bool   `json:"imported"`
	ProjectID string `json:"projectId,omitempty"`
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	gh, err := s.github()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	repos, err := gh.ListRepos(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	imported := map[string]string{}
	for _, p := range s.store.Projects() {
		if p.Repo != nil {
			imported[strings.ToLower(p.Repo.FullName())] = p.ID
		}
	}

	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := make([]repoView, 0, len(repos))
	for _, repo := range repos {
		if query != "" && !strings.Contains(strings.ToLower(repo.FullName), query) {
			continue
		}
		v := repoView{Repo: repo}
		if id, ok := imported[strings.ToLower(repo.FullName)]; ok {
			v.Imported, v.ProjectID = true, id
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// inspection is what publix can tell about a repository without cloning it.
// It is what makes the import screen able to say "this is a Next.js app,
// here is the deployment.yaml we would use" before anything is created.
type inspection struct {
	Repo      *github.Repo         `json:"repo"`
	Branches  []string             `json:"branches"`
	HasSpec   bool                 `json:"hasSpec"`
	Spec      string               `json:"spec,omitempty"`
	SpecPath  string               `json:"specPath,omitempty"`
	Detection deployspec.Detection `json:"detection"`
	// Suggested is the deployment.yaml publix would write for this repo.
	Suggested string   `json:"suggested"`
	Files     []string `json:"files"`
	Warnings  []string `json:"warnings,omitempty"`
}

func (s *Server) handleInspectRepo(w http.ResponseWriter, r *http.Request) {
	gh, err := s.github()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	owner, name := r.PathValue("owner"), r.PathValue("repo")

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	repo, err := gh.GetRepo(ctx, owner, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	ref := firstNonEmpty(r.URL.Query().Get("ref"), repo.DefaultBranch)

	out := inspection{Repo: repo}
	if branches, err := gh.ListBranches(ctx, owner, name); err == nil {
		for _, b := range branches {
			out.Branches = append(out.Branches, b.Name)
		}
	}

	entries, err := gh.ListRoot(ctx, owner, name, ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		out.Files = append(out.Files, e.Path)
		if e.Type == "file" {
			paths = append(paths, e.Path)
		}
	}

	// Detection runs against the repository over the API, reading only the
	// few config files it actually needs. It is the same code the deploy
	// runs against a checkout, so what this screen promises is what the
	// build will do.
	src := framework.NewFiles(paths, func(p string) ([]byte, error) {
		return gh.GetFile(ctx, owner, name, p, ref)
	})
	out.Detection = deployspec.DetectFrom(src)

	for _, candidate := range deployspec.Filenames {
		if !src.Exists(candidate) {
			continue
		}
		if raw, err := gh.GetFile(ctx, owner, name, candidate, ref); err == nil {
			out.HasSpec, out.Spec, out.SpecPath = true, string(raw), candidate
		}
		break
	}

	if out.HasSpec {
		if _, err := deployspec.Parse([]byte(out.Spec)); err != nil {
			out.Warnings = append(out.Warnings, "The repository's "+out.SpecPath+" could not be parsed: "+err.Error())
		}
	} else {
		out.Suggested = suggestSpec(repo.Name, out.Detection)
	}
	out.Warnings = append(out.Warnings, out.Detection.Notes...)
	writeJSON(w, http.StatusOK, out)
}

// handleImportRepo is the one-click path: create the project, register the
// webhook, and start the first deploy.
func (s *Server) handleImportRepo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Owner      string   `json:"owner"`
		Repo       string   `json:"repo"`
		Branch     string   `json:"branch"`
		Name       string   `json:"name"`
		RootDir    string   `json:"rootDir"`
		Domains    []string `json:"domains"`
		AutoDeploy *bool    `json:"autoDeploy"`
		// WriteSpec commits the suggested deployment.yaml to the repo.
		WriteSpec bool   `json:"writeSpec"`
		Spec      string `json:"spec"`
		// Deploy starts a deployment immediately after import.
		Deploy *bool `json:"deploy"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Owner == "" || body.Repo == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("owner and repo are required"))
		return
	}

	gh, err := s.github()
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	repo, err := gh.GetRepo(ctx, body.Owner, body.Repo)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	branch := firstNonEmpty(body.Branch, repo.DefaultBranch, "main")

	if existing, ok := s.store.ProjectByRepo(repo.Owner, repo.Name, ""); ok {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is already imported as the project %q", repo.FullName, existing.Name))
		return
	}

	domains, err := normaliseDomains(body.Domains)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	p := &store.Project{
		Name:        firstNonEmpty(strings.TrimSpace(body.Name), repo.Name),
		Description: repo.Description,
		RootDir:     strings.Trim(body.RootDir, "/"),
		Domains:     domains,
		AutoDeploy:  body.AutoDeploy == nil || *body.AutoDeploy,
		Repo: &store.Repo{
			Owner:    repo.Owner,
			Name:     repo.Name,
			Branch:   branch,
			CloneURL: repo.CloneURL,
			Private:  repo.Private,
		},
	}
	created, err := s.store.CreateProject(p)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}

	var warnings []string

	if body.WriteSpec && strings.TrimSpace(body.Spec) != "" {
		if _, err := deployspec.Parse([]byte(body.Spec)); err != nil {
			warnings = append(warnings, "deployment.yaml was not committed: "+err.Error())
		} else if err := gh.PutFile(ctx, repo.Owner, repo.Name, "deployment.yaml", branch,
			"Add deployment.yaml for publix", []byte(body.Spec)); err != nil {
			warnings = append(warnings, "could not commit deployment.yaml: "+err.Error())
		}
	}

	set := s.store.Settings()
	switch {
	case !created.AutoDeploy:
		// Nothing to register: the project does not want deploy-on-push.

	case set.PublicURL == "":
		warnings = append(warnings, "No public URL is configured, so a webhook could not be registered. Set it under Settings → Server, then re-import or add the webhook by hand to enable deploy-on-push.")

	case s.appDelivers(ctx, gh, r):
		// A GitHub App already receives a push event for every repository
		// it is installed on. Adding a repository webhook as well would
		// have GitHub deliver each push twice, which queues two
		// deployments for one commit.

	default:
		hookID, err := gh.EnsureHook(ctx, repo.Owner, repo.Name, s.webhookURL(r), set.GitHub.WebhookSecret)
		if err != nil {
			warnings = append(warnings, "could not create the GitHub webhook (deploy-on-push is off): "+err.Error())
		} else {
			created, _ = s.store.UpdateProject(created.ID, func(p *store.Project) error {
				p.Repo.HookID = hookID
				return nil
			})
		}
	}

	if err := s.engine.ReconcileRouting(); err != nil {
		warnings = append(warnings, err.Error())
	}

	var deployment any
	if body.Deploy == nil || *body.Deploy {
		dep, err := s.engine.Deploy(created.ID, engine.Options{Trigger: "import", Ref: branch})
		if err != nil {
			warnings = append(warnings, "the first deployment could not be started: "+err.Error())
		} else {
			deployment = dep
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"project":    s.view(created),
		"deployment": deployment,
		"warnings":   warnings,
	})
}

// handleGitHubWebhook receives push events and deploys.
//
// It is deliberately terse: verify, match, enqueue, respond. GitHub retries
// on a non-2xx and times out after ten seconds, so nothing slow belongs on
// this path.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	set := s.store.Settings()
	delivery, err := github.ParseWebhook(r, set.GitHub.WebhookSecret)
	if err != nil {
		s.log.Warn("rejected a webhook", "error", err, "from", clientIP(r))
		writeError(w, http.StatusUnauthorized, err)
		return
	}

	if delivery.Event == "ping" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pong": true})
		return
	}
	if delivery.Push == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "not a push event"})
		return
	}

	push := delivery.Push
	branch := push.Branch()
	reason := ""
	switch {
	case branch == "":
		reason = "push was to a tag, not a branch"
	case push.Deleted:
		reason = "branch was deleted"
	case push.SkipCI():
		reason = "the commit message asks to skip deployment"
	}
	if reason != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": reason})
		return
	}

	p, ok := s.store.ProjectByRepo(push.Owner(), push.Repository.Name, branch)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"ignored": fmt.Sprintf("no project deploys %s from %s", push.Repository.FullName, branch),
		})
		return
	}
	if !p.AutoDeploy {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "automatic deploys are off for this project"})
		return
	}
	if p.Paused {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": "the project is paused"})
		return
	}

	dep, err := s.engine.Deploy(p.ID, engine.Options{
		Commit:  push.After,
		Ref:     branch,
		Trigger: "push",
		Message: push.Message(),
		Author:  push.Author(),
	})
	if err != nil {
		s.log.Error("webhook deploy failed to queue", "project", p.Name, "error", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("deploying from push", "project", p.Name, "branch", branch, "commit", push.After)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "deployment": dep.ID})
}

// ReportStatus posts a deployment's outcome back to the commit on GitHub, so
// the result is visible where the change was made.
func (s *Server) ReportStatus(ctx context.Context, p *store.Project, d *store.Deployment, targetURL string) {
	if p.Repo == nil || d.Commit == "" {
		return
	}
	gh, err := s.github()
	if err != nil {
		return
	}

	st := github.CommitStatus{TargetURL: targetURL, Context: "publix"}
	switch d.Status {
	case store.StatusLive:
		st.State, st.Description = "success", "Deployed in "+d.Duration().Round(time.Second).String()
	case store.StatusFailed:
		st.State, st.Description = "failure", firstLine(d.Error)
	case store.StatusCancelled:
		st.State, st.Description = "error", "Deployment cancelled"
	default:
		st.State, st.Description = "pending", "Deploying…"
	}

	if err := gh.SetCommitStatus(ctx, p.Repo.Owner, p.Repo.Name, d.Commit, st); err != nil {
		s.log.Debug("could not post a commit status", "project", p.Name, "error", err)
	}
}

// GitAuth supplies an authenticated clone URL to the deploy engine.
func (s *Server) GitAuth(repo *store.Repo) (string, error) {
	if repo == nil || !repo.Private {
		// A public repository clones fine anonymously, and keeping a token
		// out of the git process is one less place for it to leak.
		return "", nil
	}
	gh, err := s.github()
	if err != nil {
		return "", fmt.Errorf("%s is private, but GitHub is not connected: %w", repo.FullName(), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return gh.AuthenticateCloneURL(ctx, repo.CloneURL)
}

// suggestSpec renders the deployment.yaml publix would use, which the
// import screen shows and can optionally commit to the repository.
//
// It is deliberately short. Everything publix can work out for itself is
// left out, so what remains is only what a human might want to change.
func suggestSpec(repoName string, det deployspec.Detection) string {
	sp := deployspec.Spec{Name: store.Slugify(repoName), Kind: det.Kind}

	switch det.Kind {
	case deployspec.KindCompose:
		sp.Compose, sp.Service, sp.Port = det.Compose, det.Service, det.Port
	case deployspec.KindDockerfile:
		sp.Dockerfile, sp.Port = firstNonEmpty(det.Dockerfile, "Dockerfile"), det.Port
	case deployspec.KindFramework:
		// The framework template supplies install, build and start, so the
		// file only needs to name the framework and the port.
		sp.Framework, sp.Port = det.Framework, det.Port
	case deployspec.KindStatic:
		sp.Port = det.Port
		// Only record what differs from what detection would infer again.
		if det.Framework == "" {
			sp.Build.Install, sp.Build.Command, sp.Build.Output = det.Install, det.Command, det.Output
			if det.SPA {
				spa := true
				sp.Build.SPA = &spa
			}
		} else {
			sp.Framework = det.Framework
		}
	}

	raw, err := yaml.Marshal(sp)
	if err != nil {
		return ""
	}
	head := "# Generated by publix. Edit freely — this file is the source of truth.\n"
	if det.Generated {
		head += "# publix builds this " + det.Name + " project with a generated Dockerfile;\n" +
			"# commit your own Dockerfile at any time to take over.\n"
	}
	return head + string(raw)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
