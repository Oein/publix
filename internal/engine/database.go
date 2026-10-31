package engine

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

// Labels on a database container. They deliberately do not include
// publix.project: everything that selects a project's containers by that
// label treats them as a deployment generation — to stop, reap, stream or
// health-check — and a database is none of those.
const (
	LabelDBProject = "publix.db.project"
	LabelDBID      = "publix.db.id"
	RoleDatabase   = "database"
	RoleHelper     = "db-helper"
)

// helperImage runs the small shell steps of a backup: copying, listing and
// pruning files. It is pinned so a backup does not change behaviour under
// an operator because an upstream tag moved.
const helperImage = "alpine:3.20"

// rcloneImage uploads to everywhere that is not a local directory.
const rcloneImage = "rclone/rclone:1.68"

// DatabaseStatus is a database's runtime state, for the dashboard.
type DatabaseStatus struct {
	State string `json:"state"`
	// Detail explains a state that is not "running".
	Detail string `json:"detail,omitempty"`
}

// ProvisionDatabase makes a database exist and run: its data volume, its
// image and its container. It is idempotent, so it is also how a database
// whose container was removed by hand is brought back.
func (e *Engine) ProvisionDatabase(ctx context.Context, p *store.Project, d *store.Database, w io.Writer) error {
	if w == nil {
		w = io.Discard
	}
	set := e.store.Settings()
	if _, err := e.docker.EnsureNetwork(ctx, set.Network, map[string]string{"publix.managed": "true"}); err != nil {
		return fmt.Errorf("creating the %q network: %w", set.Network, err)
	}
	if err := e.docker.EnsureVolume(ctx, d.VolumeName(p.ID), dbLabels(p, d, RoleDatabase)); err != nil {
		return fmt.Errorf("creating the data volume for %s: %w", d.Name, err)
	}

	if !d.Engine.HasServer() {
		// A fresh docker volume is owned by root and mode 755, and the
		// app writing the SQLite file is often not root. Opening it up
		// once at creation is what lets the first write succeed.
		_, err := e.runHelper(ctx, p, d, helperImage, []string{
			"sh", "-c", "chmod 0777 /db",
		}, nil, []string{d.VolumeName(p.ID) + ":/db"})
		return err
	}

	name := d.ContainerName(p.ID)
	if info, err := e.docker.InspectContainer(ctx, name); err == nil {
		if !info.State.Running {
			if err := e.docker.StartContainer(ctx, info.ID); err != nil {
				return fmt.Errorf("starting %s: %w", name, err)
			}
		}
		return e.waitDatabaseReady(ctx, p, d, 2*time.Minute)
	} else if !dockerapi.NotFound(err) {
		return err
	}

	fmt.Fprintf(w, "Pulling %s\n", d.Image())
	if err := e.ensureImage(ctx, d.Image(), w); err != nil {
		return err
	}

	cfg := e.databaseConfig(&set, p, d)
	created, err := e.docker.CreateContainer(ctx, name, cfg)
	if err != nil {
		return fmt.Errorf("creating %s: %w", name, err)
	}
	if err := e.docker.StartContainer(ctx, created.ID); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	fmt.Fprintf(w, "Started %s, waiting for it to accept connections\n", name)
	// A first start initialises the data directory, which for MySQL takes
	// most of a minute. An app deployed before that is over fails its
	// health check on a refused connection, for no fault of its own.
	return e.waitDatabaseReady(ctx, p, d, 3*time.Minute)
}

// databaseConfig is the container a server database runs as.
func (e *Engine) databaseConfig(set *store.Settings, p *store.Project, d *store.Database) *dockerapi.CreateConfig {
	vol := d.VolumeName(p.ID)
	cfg := &dockerapi.CreateConfig{
		Image:  d.Image(),
		Labels: dbLabels(p, d, RoleDatabase),
		HostConfig: &dockerapi.HostConfig{
			NetworkMode:   set.Network,
			RestartPolicy: &dockerapi.RestartPolicy{Name: "unless-stopped"},
		},
		NetworkingConfig: &dockerapi.NetworkingConfig{
			EndpointsConfig: map[string]*dockerapi.EndpointSettings{
				set.Network: {Aliases: []string{d.ContainerName(p.ID)}},
			},
		},
	}
	if set.LogDriver != "" {
		cfg.HostConfig.LogConfig = &dockerapi.LogConfig{Type: set.LogDriver, Config: set.LogOptions}
	}

	switch d.Engine {
	case store.EnginePostgres:
		cfg.Env = []string{
			"POSTGRES_USER=" + d.User,
			"POSTGRES_PASSWORD=" + d.Password,
			"POSTGRES_DB=" + d.DBName,
		}
		cfg.HostConfig.Binds = []string{vol + ":" + postgresDataDir(d.Version)}
	case store.EngineMySQL:
		cfg.Env = []string{
			"MYSQL_ROOT_PASSWORD=" + d.Password,
			"MYSQL_DATABASE=" + d.DBName,
			"MYSQL_USER=" + d.User,
			"MYSQL_PASSWORD=" + d.Password,
		}
		cfg.HostConfig.Binds = []string{vol + ":/var/lib/mysql"}
	case store.EngineMongo:
		cfg.Env = []string{
			"MONGO_INITDB_ROOT_USERNAME=" + d.User,
			"MONGO_INITDB_ROOT_PASSWORD=" + d.Password,
			"MONGO_INITDB_DATABASE=" + d.DBName,
		}
		cfg.HostConfig.Binds = []string{vol + ":/data/db"}
	case store.EngineRedis:
		// Without appendonly a restart loses everything since the last
		// snapshot, which is not what anyone asking for a database
		// rather than a cache expects.
		cfg.Cmd = []string{"redis-server", "--requirepass", d.Password, "--appendonly", "yes"}
		cfg.HostConfig.Binds = []string{vol + ":/data"}
	}
	return cfg
}

// postgresDataDir is where the image keeps its data. Postgres 18 moved it
// one level up, and mounting the old path there makes the container refuse
// to start rather than silently use an unmounted directory.
func postgresDataDir(version string) string {
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if major >= 18 {
		return "/var/lib/postgresql"
	}
	return "/var/lib/postgresql/data"
}

func dbLabels(p *store.Project, d *store.Database, role string) map[string]string {
	return map[string]string{
		"publix.managed": "true",
		"publix.role":    role,
		LabelDBProject:   p.ID,
		LabelDBID:        d.ID,
	}
}

// readyCommand is a command that succeeds inside the database container
// once it accepts connections.
func readyCommand(d *store.Database) []string {
	switch d.Engine {
	case store.EnginePostgres:
		return []string{"pg_isready", "-h", "127.0.0.1", "-U", d.User, "-d", d.DBName}
	case store.EngineMySQL:
		// Checking over TCP rather than the socket matters: the image's
		// init runs a temporary server on the socket only, which answers
		// a socket ping long before the real one is up.
		return []string{"sh", "-c", `mysqladmin ping -h 127.0.0.1 -uroot -p"$MYSQL_ROOT_PASSWORD" --silent`}
	case store.EngineMongo:
		return []string{"mongosh", "--quiet", "--eval", "db.adminCommand('ping').ok"}
	case store.EngineRedis:
		return []string{"redis-cli", "-a", d.Password, "--no-auth-warning", "ping"}
	}
	return nil
}

// waitDatabaseReady blocks until the database accepts connections.
func (e *Engine) waitDatabaseReady(ctx context.Context, p *store.Project, d *store.Database, limit time.Duration) error {
	cmd := readyCommand(d)
	if cmd == nil {
		return nil
	}
	name := d.ContainerName(p.ID)
	deadline := time.Now().Add(limit)
	var last string
	for {
		res, err := e.docker.Exec(ctx, name, cmd)
		switch {
		case err != nil:
			last = err.Error()
		case res.ExitCode == 0:
			return nil
		default:
			last = strings.TrimSpace(res.Stderr + " " + res.Stdout)
		}
		// A server that keeps dying will never become ready, and the
		// reason is in its own output — quote it rather than wait out the
		// whole limit and report a refused connection.
		if info, ierr := e.docker.InspectContainer(ctx, name); ierr == nil &&
			((!info.State.Running && !info.State.Restarting) || info.RestartCount >= 2) {
			return fmt.Errorf("the %s database %q keeps exiting (code %d) before it is ready. Its last output:\n%s",
				d.Engine, d.Name, info.State.ExitCode, e.containerTail(ctx, name))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the %s database %q did not become ready within %s: %s\n%s", d.Engine, d.Name, limit, last, e.containerTail(ctx, name))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// containerTail is the last few lines a container wrote, for an error.
func (e *Engine) containerTail(ctx context.Context, name string) string {
	var buf strings.Builder
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = e.docker.ContainerLogs(tctx, name, dockerapi.LogOptions{Stdout: true, Stderr: true, Tail: "5"}, &buf)
	return strings.TrimSpace(buf.String())
}

// RemoveDatabase removes a database's container, and its data too when
// deleteData is set. Keeping the data is the default for the same reason it
// is for volumes: deleting configuration must never silently delete data.
func (e *Engine) RemoveDatabase(ctx context.Context, p *store.Project, d *store.Database, deleteData bool) error {
	if d.Engine.HasServer() {
		if err := e.docker.RemoveContainer(ctx, d.ContainerName(p.ID), true, false); err != nil {
			return err
		}
	}
	if deleteData {
		if err := e.docker.RemoveVolume(ctx, d.VolumeName(p.ID), true); err != nil {
			return fmt.Errorf("removing the data volume %s: %w", d.VolumeName(p.ID), err)
		}
	}
	return nil
}

// RestartDatabase restarts a database's container.
func (e *Engine) RestartDatabase(ctx context.Context, p *store.Project, d *store.Database) error {
	if !d.Engine.HasServer() {
		return fmt.Errorf("%s is an SQLite database: there is no server to restart", d.Name)
	}
	name := d.ContainerName(p.ID)
	if err := e.docker.StopContainer(ctx, name, 30*time.Second); err != nil && !dockerapi.NotFound(err) {
		return err
	}
	return e.ProvisionDatabase(ctx, p, d, nil)
}

// DatabaseState reports whether a database's container is running.
func (e *Engine) DatabaseState(ctx context.Context, p *store.Project, d *store.Database) DatabaseStatus {
	if !d.Engine.HasServer() {
		return DatabaseStatus{State: "ready"}
	}
	info, err := e.docker.InspectContainer(ctx, d.ContainerName(p.ID))
	switch {
	case dockerapi.NotFound(err):
		return DatabaseStatus{State: "missing", Detail: "the container does not exist; restarting the database recreates it"}
	case err != nil:
		return DatabaseStatus{State: "unknown", Detail: err.Error()}
	case info.State.Restarting:
		return DatabaseStatus{State: "restarting", Detail: fmt.Sprintf("restarted %d times — it is probably crash-looping", info.RestartCount)}
	case info.State.Running:
		return DatabaseStatus{State: "running"}
	default:
		return DatabaseStatus{State: "stopped", Detail: fmt.Sprintf("exited with code %d", info.State.ExitCode)}
	}
}

// DatabaseLogs writes the tail of a database container's output.
func (e *Engine) DatabaseLogs(ctx context.Context, p *store.Project, d *store.Database, tail string, w io.Writer) error {
	if !d.Engine.HasServer() {
		return fmt.Errorf("%s is an SQLite database: it has no server and no log", d.Name)
	}
	return e.docker.ContainerLogs(ctx, d.ContainerName(p.ID), dockerapi.LogOptions{
		Stdout: true, Stderr: true, Tail: tail, Timestamps: true,
	}, w)
}

// EnsureDatabases brings back any database whose container has gone
// missing, which is what a host restored from a backup of /var/lib/publix,
// or a careless `docker rm`, leaves behind. It runs at startup.
func (e *Engine) EnsureDatabases(ctx context.Context) []error {
	var errs []error
	for _, p := range e.store.Projects() {
		for _, d := range p.Databases {
			if !d.Engine.HasServer() {
				continue
			}
			if _, err := e.docker.InspectContainer(ctx, d.ContainerName(p.ID)); err == nil || !dockerapi.NotFound(err) {
				continue
			}
			if err := e.ProvisionDatabase(ctx, p, d, nil); err != nil {
				errs = append(errs, fmt.Errorf("%s/%s: %w", p.Name, d.Name, err))
			}
		}
	}
	return errs
}

// teardownDatabases removes every database container of a project. Their
// data volumes are kept, like every other piece of project data.
func (e *Engine) teardownDatabases(ctx context.Context, p *store.Project) []string {
	var problems []string
	for _, d := range p.Databases {
		if err := e.RemoveDatabase(ctx, p, d, false); err != nil {
			problems = append(problems, "removing database "+d.Name+": "+err.Error())
		}
	}
	return problems
}

// ensureDeclaredDatabases creates every database deployment.yaml asks for
// that the project does not have yet, before anything is built.
//
// It only ever adds. A database that disappears from the file is left
// running with its data: removing a line from a YAML file is far too easy
// a way to destroy a database, so that stays an explicit act in the
// dashboard.
func (e *Engine) ensureDeclaredDatabases(ctx context.Context, dc *Context) error {
	if dc.Spec == nil || len(dc.Spec.Databases) == 0 {
		return nil
	}
	for _, want := range dc.Spec.Databases {
		p, _ := e.store.Project(dc.Project.ID)
		if existing, ok := p.Database(want.Name); ok {
			if existing.Engine != store.DBEngine(want.Engine) {
				return fmt.Errorf("deployment.yaml declares %q as %s, but the project already has a %s database by that name",
					want.Name, want.Engine, existing.Engine)
			}
			// Make sure it is actually running: a deploy is the moment
			// someone finds out it is not.
			if err := e.ProvisionDatabase(ctx, p, existing, dc.Log.Writer("stdout")); err != nil {
				return err
			}
			continue
		}

		d, err := p.NewDatabase(want.Name, store.DBEngine(want.Engine), want.Version, want.EnvPrefix)
		if err != nil {
			return fmt.Errorf("databases: %w", err)
		}
		d.Declared = true
		dc.Log.Printf("Creating the %s database %q (from deployment.yaml)", d.Engine, d.Name)
		if _, err := e.AddDatabase(ctx, p.ID, d, dc.Log.Writer("stdout")); err != nil {
			return err
		}
		dc.Log.Printf("Database %q is ready; its connection details are in %s", d.Name, strings.Join(d.EnvKeys(), ", "))
	}
	if p, ok := e.store.Project(dc.Project.ID); ok {
		dc.Project = p
	}
	return nil
}

// AddDatabase records a database on a project and provisions it. If it
// cannot be started the record is removed again, so a failed attempt does
// not leave a DATABASE_URL pointing at nothing.
func (e *Engine) AddDatabase(ctx context.Context, projectID string, d *store.Database, w io.Writer) (*store.Project, error) {
	p, err := e.store.UpdateProject(projectID, func(p *store.Project) error {
		if err := p.ValidateDatabase(d, ""); err != nil {
			return err
		}
		p.Databases = append(p.Databases, d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := e.ProvisionDatabase(ctx, p, d, w); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		_ = e.RemoveDatabase(cleanup, p, d, true)
		_, _ = e.store.UpdateProject(projectID, func(p *store.Project) error {
			p.Databases = dropDatabase(p.Databases, d.ID)
			return nil
		})
		return nil, err
	}
	return p, nil
}

func dropDatabase(in []*store.Database, id string) []*store.Database {
	out := make([]*store.Database, 0, len(in))
	for _, d := range in {
		if d.ID != id {
			out = append(out, d)
		}
	}
	return out
}

// databaseBinds mounts each SQLite database's directory into the project's
// containers. Server databases need no mount: they are reached over the
// network.
func databaseBinds(p *store.Project) []Bind {
	var out []Bind
	for _, d := range p.Databases {
		if d.Engine != store.EngineSQLite {
			continue
		}
		out = append(out, Bind{
			Volume:    "database " + d.Name,
			HostPath:  d.VolumeName(p.ID),
			MountPath: d.SQLiteDir(),
			Named:     true,
		})
	}
	return out
}

// ensureImage pulls an image unless it is already on the host.
func (e *Engine) ensureImage(ctx context.Context, ref string, w io.Writer) error {
	if ok, err := e.docker.ImageExists(ctx, ref); err == nil && ok {
		return nil
	}
	if w == nil {
		w = io.Discard
	}
	if err := e.docker.PullImage(ctx, ref, w); err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	return nil
}
