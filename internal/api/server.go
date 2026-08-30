// Package api serves the dashboard and its REST interface.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/github"
	"github.com/Oein/publix/internal/store"
)

// Server is the platform's HTTP interface.
type Server struct {
	store  *store.Store
	engine *engine.Engine
	docker *dockerapi.Client
	log    *slog.Logger

	// assets is the built dashboard, embedded in the binary.
	assets fs.FS

	loginLimiter *limiter

	mu sync.Mutex
	// gh caches the GitHub client, rebuilt when credentials change.
	gh        *github.Client
	ghFingerp string
}

// Options configure the server.
type Options struct {
	Store  *store.Store
	Engine *engine.Engine
	Docker *dockerapi.Client
	Assets fs.FS
	Logger *slog.Logger
}

// New creates a Server.
func New(opt Options) *Server {
	log := opt.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store:        opt.Store,
		engine:       opt.Engine,
		docker:       opt.Docker,
		assets:       opt.Assets,
		log:          log,
		loginLimiter: newLimiter(8, time.Minute),
	}
}

// Handler builds the complete HTTP routing tree.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated: the webhook proves itself with an HMAC signature,
	// and the auth endpoints are how a session is obtained in the first
	// place.
	mux.HandleFunc("POST /api/webhooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("GET /api/auth", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	auth := s.requireAuth
	mux.HandleFunc("POST /api/auth/password", auth(s.handleChangePassword))

	mux.HandleFunc("GET /api/projects", auth(s.handleListProjects))
	mux.HandleFunc("POST /api/projects", auth(s.handleCreateProject))
	mux.HandleFunc("GET /api/projects/{id}", auth(s.handleGetProject))
	mux.HandleFunc("PATCH /api/projects/{id}", auth(s.handleUpdateProject))
	mux.HandleFunc("DELETE /api/projects/{id}", auth(s.handleDeleteProject))
	mux.HandleFunc("POST /api/projects/{id}/deploy", auth(s.handleDeploy))
	mux.HandleFunc("POST /api/projects/{id}/rollback", auth(s.handleRollback))
	mux.HandleFunc("GET /api/projects/{id}/rollback-plan", auth(s.handleRollbackPlan))
	mux.HandleFunc("POST /api/projects/{id}/cancel", auth(s.handleCancel))
	mux.HandleFunc("GET /api/projects/{id}/deployments/{did}", auth(s.handleGetDeployment))
	mux.HandleFunc("GET /api/projects/{id}/deployments/{did}/logs", auth(s.handleBuildLogs))
	mux.HandleFunc("GET /api/projects/{id}/runtime-logs", auth(s.handleRuntimeLogs))
	mux.HandleFunc("GET /api/projects/{id}/containers", auth(s.handleContainers))
	mux.HandleFunc("PUT /api/projects/{id}/env", auth(s.handleSetEnv))
	mux.HandleFunc("PUT /api/projects/{id}/domains", auth(s.handleSetDomains))
	mux.HandleFunc("POST /api/projects/{id}/cron/{job}/run", auth(s.handleRunCron))

	mux.HandleFunc("GET /api/github", auth(s.handleGitHubStatus))
	mux.HandleFunc("PUT /api/github", auth(s.handleSetGitHub))
	mux.HandleFunc("DELETE /api/github", auth(s.handleDisconnectGitHub))
	mux.HandleFunc("GET /api/github/repos", auth(s.handleListRepos))
	mux.HandleFunc("GET /api/github/repos/{owner}/{repo}/inspect", auth(s.handleInspectRepo))
	mux.HandleFunc("POST /api/github/import", auth(s.handleImportRepo))

	mux.HandleFunc("GET /api/settings", auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", auth(s.handleSetSettings))
	mux.HandleFunc("POST /api/volumes", auth(s.handleAddVolume))
	mux.HandleFunc("DELETE /api/volumes/{name}", auth(s.handleDeleteVolume))
	mux.HandleFunc("GET /api/system", auth(s.handleSystem))
	mux.HandleFunc("GET /api/events", auth(s.handleEvents))

	mux.HandleFunc("/", s.handleAssets)

	return s.middleware(mux)
}

// middleware adds logging, panic recovery and security headers.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rv := recover(); rv != nil {
				// A panic in one handler must not take down the whole
				// control plane and every deploy queued behind it.
				s.log.Error("panic serving request",
					"method", r.Method, "path", r.URL.Path, "panic", rv)
				if !rec.wrote {
					writeError(w, http.StatusInternalServerError, errors.New("internal error"))
				}
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				s.log.Debug("request",
					"method", r.Method, "path", r.URL.Path,
					"status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
			}
		}()

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer so SSE streaming still works.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// github returns a GitHub client for the current credentials, rebuilding it
// when the stored settings change.
func (s *Server) github() (*github.Client, error) {
	set := s.store.Settings().GitHub
	fingerprint := set.Token + "|" + set.AppID + "|" + set.InstallationID + "|" + set.APIBase + "|" + fmt.Sprint(len(set.PrivateKey))

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gh != nil && s.ghFingerp == fingerprint {
		return s.gh, nil
	}
	c, err := github.New(set)
	if err != nil {
		return nil, err
	}
	s.gh, s.ghFingerp = c, fingerprint
	return c, nil
}

// JSON helpers.

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil && !isBrokenPipe(err) {
		slog.Debug("writing json response", "error", err)
	}
}

// errorBody is the shape every failure is reported in, so the dashboard has
// exactly one thing to render.
type errorBody struct {
	Error string `json:"error"`
	// Details carries a multi-line explanation, such as a list of spec
	// validation problems, without the summary line being lost.
	Details []string `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, err error) {
	body := errorBody{Error: err.Error()}
	// A multi-line error reads badly in a toast; split it so the UI can
	// show a headline and a list.
	if lines := strings.Split(err.Error(), "\n"); len(lines) > 1 {
		body.Error = strings.TrimSpace(lines[0])
		for _, l := range lines[1:] {
			if t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- ")); t != "" {
				body.Details = append(body.Details, t)
			}
		}
	}
	writeJSON(w, status, body)
}

// readJSON decodes a request body, rejecting unknown fields so a typo in a
// client payload is an error rather than a silently ignored setting.
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func isBrokenPipe(err error) bool {
	s := err.Error()
	return strings.Contains(s, "broken pipe") || strings.Contains(s, "connection reset")
}

// statusFor maps a domain error onto an HTTP status.
func statusFor(err error) int {
	switch {
	case store.IsNotFound(err):
		return http.StatusNotFound
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	case errors.Is(err, github.ErrNotConfigured):
		return http.StatusPreconditionFailed
	default:
		return http.StatusBadRequest
	}
}

// limiter is a small fixed-window rate limiter, used only for login.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	count int
	start time.Time
}

func newLimiter(max int, per time.Duration) *limiter {
	return &limiter{max: max, window: per, hits: map[string]*window{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		l.hits[key] = &window{count: 1, start: now}
		// Opportunistically drop stale entries so a stream of distinct
		// source addresses cannot grow this map without bound.
		if len(l.hits) > 1024 {
			for k, v := range l.hits {
				if now.Sub(v.start) > l.window {
					delete(l.hits, k)
				}
			}
		}
		return true
	}
	w.count++
	return w.count <= l.max
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
