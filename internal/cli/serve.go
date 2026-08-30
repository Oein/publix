package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Oein/publix/internal/api"
	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
)

var buildVersion = "dev"

func setVersion(v string) {
	buildVersion = v
	api.Version = v
}

// platform is everything the server needs, wired together.
type platform struct {
	store  *store.Store
	docker *dockerapi.Client
	logs   *buildlog.Store
	engine *engine.Engine
	log    *slog.Logger
}

// open builds the platform, failing early and legibly on anything missing.
func open(ctx context.Context, verbose bool) (*platform, error) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	st, err := store.Open()
	if err != nil {
		return nil, err
	}

	docker, err := dockerapi.New()
	if err != nil {
		return nil, err
	}
	if _, err := docker.Ping(ctx); err != nil {
		return nil, fmt.Errorf("cannot talk to Docker at %s: %w", docker.Host(), err)
	}

	set := st.Settings()
	logs, err := buildlog.NewStore(filepath.Join(store.Home(), "logs"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(set.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating the work directory %s: %w", set.WorkDir, err)
	}

	return &platform{store: st, docker: docker, logs: logs, engine: engine.New(st, docker, logs), log: log}, nil
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flagSet("serve")
	addr := fs.String("addr", envOr("PUBLIX_ADDR", "127.0.0.1:4321"), "address to listen on")
	verbose := fs.Bool("v", false, "verbose logging")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}

	p, err := open(ctx, *verbose)
	if err != nil {
		return err
	}

	srv := api.New(api.Options{
		Store:  p.store,
		Engine: p.engine,
		Docker: p.docker,
		Assets: api.Dashboard(),
		Logger: p.log,
	})
	// The engine needs GitHub for private clones and commit statuses, and
	// the API layer owns the GitHub client, so they are wired to each other.
	p.engine.GitAuth = srv.GitAuth
	p.engine.StatusReporter = srv

	// Bring routing in line with reality at startup: the store may have
	// been edited, or Traefik's file lost, while publix was not running.
	if err := p.engine.ReconcileRouting(); err != nil {
		p.log.Warn("could not write Traefik's routing file", "error", err)
	}

	scheduler := engine.NewScheduler(p.engine, p.store)
	scheduler.OnError = func(project, job string, err error) {
		p.log.Error("scheduled job failed", "project", project, "job", job, "error", err)
	}
	go scheduler.Run(ctx)

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
		// Deploy logs stream for as long as a build runs, so there can be
		// no write timeout; the read side stays bounded.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", *addr, err)
	}

	set := p.store.Settings()
	fmt.Printf("publix %s\n", buildVersion)
	fmt.Printf("  dashboard   http://%s\n", friendlyAddr(ln.Addr().String()))
	fmt.Printf("  docker      %s\n", p.docker.Host())
	fmt.Printf("  state       %s\n", p.store.Path())
	fmt.Printf("  traefik     %s\n", filepath.Join(set.TraefikDynamicDir, "publix.yml"))
	if set.Auth.PasswordHash == "" {
		fmt.Printf("\n  Open the dashboard to choose an admin password.\n")
	}
	if set.PublicURL == "" {
		fmt.Printf("  Set a public URL in Settings to enable GitHub deploy-on-push.\n")
	}
	fmt.Println()

	errs := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	fmt.Println("shutting down…")
	// Deployments in flight are left to finish: killing a build mid-way
	// can leave a half-started generation behind.
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

func friendlyAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		return "localhost:" + port
	}
	return addr
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
