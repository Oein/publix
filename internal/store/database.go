package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DBEngine is the kind of database a project can be given.
type DBEngine string

// Supported database engines.
const (
	EnginePostgres DBEngine = "postgres"
	EngineMySQL    DBEngine = "mysql"
	EngineRedis    DBEngine = "redis"
	EngineMongo    DBEngine = "mongo"
	// EngineSQLite has no server at all: it is a directory that survives
	// redeploys, mounted into every container of the project, with the
	// database file's path handed over in the environment.
	EngineSQLite DBEngine = "sqlite"
)

// DBEngines lists every engine, in the order the dashboard offers them.
var DBEngines = []DBEngine{EnginePostgres, EngineMySQL, EngineMongo, EngineRedis, EngineSQLite}

// Database is a database publix runs for one project.
//
// It belongs to the project rather than to any deployment: it is started
// once, outlives every redeploy and rollback, and its connection details
// reach the project's containers as environment variables. Nothing about it
// has to be configured by hand, which is the point — "add a Postgres" should
// be one action, not a container, a volume, a password and a DATABASE_URL.
type Database struct {
	// ID is opaque and never changes. The data volume is named after it,
	// so deleting a database and creating another with the same name can
	// never pick up the old one's data — or its old password.
	ID string `json:"id"`

	// Name identifies the database within its project, e.g. "main".
	Name string `json:"name"`

	Engine DBEngine `json:"engine"`

	// Version is the image tag, e.g. "17" for postgres:17-alpine.
	Version string `json:"version,omitempty"`

	// EnvPrefix decides the variable names: "DATABASE" yields DATABASE_URL,
	// DATABASE_HOST and so on.
	EnvPrefix string `json:"envPrefix"`

	// User, Password and DBName are generated at creation and fixed from
	// then on: a server engine records them in its data directory on first
	// start, and changing them here afterwards would only break the URL.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	DBName   string `json:"dbName,omitempty"`

	// Declared marks a database created because deployment.yaml asked for
	// it, rather than from the dashboard.
	Declared bool `json:"declared,omitempty"`

	// Backup is the automatic backup policy. Nil means none.
	Backup *BackupPolicy `json:"backup,omitempty"`

	// Backups is the recent history of backups and restores, newest first.
	Backups []BackupRecord `json:"backups,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
}

// BackupPolicy schedules a database's backups.
type BackupPolicy struct {
	// Schedule is a five-field cron expression, or @daily and friends.
	Schedule string `json:"schedule"`
	// Target is the name of a registered backup target.
	Target string `json:"target"`
	// Keep is how many backups to retain on the target. Older ones are
	// deleted after each successful backup.
	Keep int `json:"keep,omitempty"`
}

// BackupRecord is one backup or restore attempt.
type BackupRecord struct {
	ID string `json:"id"`
	// Kind is "backup" or "restore".
	Kind    string `json:"kind"`
	Trigger string `json:"trigger,omitempty"`
	Target  string `json:"target"`
	// File is the backup's name on the target.
	File   string `json:"file,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`

	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Backup record statuses.
const (
	BackupRunning   = "running"
	BackupSucceeded = "succeeded"
	BackupFailed    = "failed"
)

// keepBackupRecords bounds the history kept per database. The backups
// themselves live on the target; this is only what the dashboard lists.
const keepBackupRecords = 30

// AddBackupRecord puts a record at the head of the history.
func (d *Database) AddBackupRecord(r BackupRecord) {
	d.Backups = append([]BackupRecord{r}, d.Backups...)
	if len(d.Backups) > keepBackupRecords {
		d.Backups = d.Backups[:keepBackupRecords]
	}
}

// BackupRecord finds a record by ID.
func (d *Database) BackupRecord(id string) *BackupRecord {
	for i := range d.Backups {
		if d.Backups[i].ID == id {
			return &d.Backups[i]
		}
	}
	return nil
}

// DefaultVersion is the version a database gets when none is asked for.
func (e DBEngine) DefaultVersion() string {
	switch e {
	case EnginePostgres:
		return "17"
	case EngineMySQL:
		return "8.4"
	case EngineMongo:
		// Not "8": that tag is 8.0, which refuses to start at all on
		// Linux 6.19 and newer (SERVER-121912). 8.2 does not.
		return "8.2"
	case EngineRedis:
		return "7"
	}
	return ""
}

// Port is the port the engine listens on inside its container.
func (e DBEngine) Port() int {
	switch e {
	case EnginePostgres:
		return 5432
	case EngineMySQL:
		return 3306
	case EngineMongo:
		return 27017
	case EngineRedis:
		return 6379
	}
	return 0
}

// HasServer reports whether the engine runs as a container of its own.
func (e DBEngine) HasServer() bool { return e != EngineSQLite }

// IsSQL reports whether e answers SQL, which is what the explorer's query
// box speaks.
func (e DBEngine) IsSQL() bool {
	return e == EnginePostgres || e == EngineMySQL || e == EngineSQLite
}

// Valid reports whether e is a supported engine.
func (e DBEngine) Valid() bool {
	for _, x := range DBEngines {
		if x == e {
			return true
		}
	}
	return false
}

// conventionalPrefix is the prefix the first database of an engine gets,
// chosen so the variable it produces is the one frameworks already read:
// DATABASE_URL for Prisma, Django, Rails and nearly everything else;
// REDIS_URL for every Redis client that reads one at all.
func (e DBEngine) conventionalPrefix() string {
	switch e {
	case EngineRedis:
		return "REDIS"
	case EngineMongo:
		return "MONGODB"
	}
	return "DATABASE"
}

// Image is the container image the database runs.
func (d *Database) Image() string {
	v := d.Version
	if v == "" {
		v = d.Engine.DefaultVersion()
	}
	switch d.Engine {
	case EnginePostgres:
		return "postgres:" + v + "-alpine"
	case EngineMySQL:
		return "mysql:" + v
	case EngineMongo:
		return "mongo:" + v
	case EngineRedis:
		return "redis:" + v + "-alpine"
	}
	return ""
}

// ContainerName is the database container's name, and also its hostname on
// the shared network. It is built from IDs rather than slugs so renaming a
// project does not move the address its DATABASE_URL points at.
func (d *Database) ContainerName(projectID string) string {
	return "publix-db-" + projectID + "-" + d.Name
}

// Host is what the project's containers connect to.
func (d *Database) Host(projectID string) string { return d.ContainerName(projectID) }

// VolumeName is the docker volume holding the database's data.
func (d *Database) VolumeName(projectID string) string {
	return "publix-db-" + projectID + "-" + d.ID
}

// SQLiteDir is where an SQLite database's directory is mounted inside the
// project's containers.
func (d *Database) SQLiteDir() string { return "/data/" + d.Name }

// SQLitePath is the database file inside that directory.
func (d *Database) SQLitePath() string { return filepath.Join(d.SQLiteDir(), d.Name+".db") }

// URL is the connection string the project receives.
func (d *Database) URL(projectID string) string {
	host := d.Host(projectID)
	switch d.Engine {
	case EnginePostgres:
		// The server has no certificate — it is reached only over the
		// private network — and lib/pq, which golang-migrate and much else
		// still use, defaults to sslmode=require and refuses to connect.
		return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable", d.User, d.Password, host, d.Engine.Port(), d.DBName)
	case EngineMySQL:
		return fmt.Sprintf("mysql://%s:%s@%s:%d/%s", d.User, d.Password, host, d.Engine.Port(), d.DBName)
	case EngineMongo:
		// The user is created in the admin database by the image's init,
		// so authSource has to say so or every login is refused.
		return fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=admin", d.User, d.Password, host, d.Engine.Port(), d.DBName)
	case EngineRedis:
		return fmt.Sprintf("redis://default:%s@%s:%d/0", d.Password, host, d.Engine.Port())
	case EngineSQLite:
		// Prisma, Drizzle and libsql all read the file: form. The bare
		// path is in <PREFIX>_PATH for everything that does not.
		return "file:" + d.SQLitePath()
	}
	return ""
}

// Env is every variable the database contributes to its project.
func (d *Database) Env(projectID string) map[string]string {
	p := d.EnvPrefix
	out := map[string]string{p + "_URL": d.URL(projectID)}
	switch d.Engine {
	case EngineSQLite:
		out[p+"_PATH"] = d.SQLitePath()
	case EngineMongo:
		// Mongo's own drivers and docs say URI, not URL; give both so
		// MONGODB_URI works without anyone having to know that.
		out[p+"_URI"] = d.URL(projectID)
		out[p+"_HOST"] = d.Host(projectID)
		out[p+"_PORT"] = fmt.Sprint(d.Engine.Port())
		out[p+"_USER"] = d.User
		out[p+"_PASSWORD"] = d.Password
		out[p+"_NAME"] = d.DBName
	case EngineRedis:
		out[p+"_HOST"] = d.Host(projectID)
		out[p+"_PORT"] = fmt.Sprint(d.Engine.Port())
		out[p+"_PASSWORD"] = d.Password
	default:
		out[p+"_HOST"] = d.Host(projectID)
		out[p+"_PORT"] = fmt.Sprint(d.Engine.Port())
		out[p+"_USER"] = d.User
		out[p+"_PASSWORD"] = d.Password
		out[p+"_NAME"] = d.DBName
	}
	return out
}

// EnvKeys lists the variable names a database contributes, sorted.
func (d *Database) EnvKeys() []string {
	env := d.Env("x")
	out := make([]string, 0, len(env))
	for k := range env {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Database finds one of a project's databases by name or ID.
func (p *Project) Database(nameOrID string) (*Database, bool) {
	for _, d := range p.Databases {
		if d.Name == nameOrID || d.ID == nameOrID {
			return d, true
		}
	}
	return nil, false
}

// DatabaseEnv is every variable the project's databases contribute.
func (p *Project) DatabaseEnv() map[string]string {
	out := map[string]string{}
	for _, d := range p.Databases {
		for k, v := range d.Env(p.ID) {
			out[k] = v
		}
	}
	return out
}

var dbNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

var envPrefixRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,40}$`)

// NewDatabase prepares a database record for a project, filling in every
// default and generating its credentials. It does not add it to the project.
func (p *Project) NewDatabase(name string, engine DBEngine, version, prefix string) (*Database, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "main"
		if engine == EngineRedis {
			name = "cache"
		}
	}
	d := &Database{
		ID:        NewID(),
		Name:      name,
		Engine:    engine,
		Version:   strings.TrimSpace(version),
		EnvPrefix: strings.ToUpper(strings.TrimSpace(prefix)),
		CreatedAt: time.Now().UTC(),
	}
	if d.Version == "" {
		d.Version = engine.DefaultVersion()
	}
	if d.EnvPrefix == "" {
		d.EnvPrefix = p.freePrefix(engine, name)
	}
	if engine.HasServer() {
		// Hex only: it never needs escaping in a URL, which is what
		// keeps DATABASE_URL usable without any percent-encoding.
		d.Password = NewToken()[:32]
	}
	if engine == EnginePostgres || engine == EngineMySQL || engine == EngineMongo {
		d.User = "app"
		d.DBName = strings.ReplaceAll(name, "-", "_")
	}
	if err := p.ValidateDatabase(d, ""); err != nil {
		return nil, err
	}
	return d, nil
}

// freePrefix picks the conventional prefix if no other database holds it,
// and otherwise one named after the database.
func (p *Project) freePrefix(engine DBEngine, name string) string {
	want := engine.conventionalPrefix()
	for _, d := range p.Databases {
		if d.EnvPrefix == want {
			return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		}
	}
	return want
}

// ValidateDatabase checks a database record against the rest of the project.
// editing is the ID of the database being replaced, if any.
func (p *Project) ValidateDatabase(d *Database, editing string) error {
	var errs []string
	if !dbNameRe.MatchString(d.Name) {
		errs = append(errs, fmt.Sprintf("name %q must start with a letter and be lowercase letters, digits and dashes (at most 31)", d.Name))
	}
	if !d.Engine.Valid() {
		errs = append(errs, fmt.Sprintf("engine %q is not one of postgres, mysql, mongo, redis, sqlite", d.Engine))
	}
	if d.Version != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`).MatchString(d.Version) {
		errs = append(errs, fmt.Sprintf("version %q is not an image tag", d.Version))
	}
	if !envPrefixRe.MatchString(d.EnvPrefix) {
		errs = append(errs, fmt.Sprintf("envPrefix %q must be uppercase letters, digits and underscores, starting with a letter", d.EnvPrefix))
	}
	for _, other := range p.Databases {
		if other.ID == editing || other.ID == d.ID {
			continue
		}
		if other.Name == d.Name {
			errs = append(errs, fmt.Sprintf("this project already has a database named %q", d.Name))
		}
		if other.EnvPrefix == d.EnvPrefix {
			errs = append(errs, fmt.Sprintf("the database %q already uses the prefix %s; two databases cannot both set %s_URL", other.Name, d.EnvPrefix, d.EnvPrefix))
		}
	}
	if d.Backup != nil {
		if d.Backup.Target == "" {
			errs = append(errs, "a backup schedule needs a target")
		}
		if d.Backup.Keep < 0 {
			errs = append(errs, "backup.keep cannot be negative")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid database:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// BackupTargetType is where a backup target puts its files.
type BackupTargetType string

// Backup target types.
const (
	// TargetLocal writes into a directory on the host: a second disk, an
	// NFS mount, a USB drive.
	TargetLocal BackupTargetType = "local"
	// TargetRclone uploads with rclone, which is how one target type
	// covers Google Drive, S3, Backblaze, Dropbox, OneDrive, SFTP and
	// forty-odd more without publix knowing any of their APIs.
	TargetRclone BackupTargetType = "rclone"
)

// BackupTarget is somewhere database backups are written to. Targets are
// server settings: where the backups go is the operator's decision, and a
// target's credentials are not something a project should hold.
type BackupTarget struct {
	Name string           `json:"name"`
	Type BackupTargetType `json:"type"`

	// Path is the host directory, for a local target.
	Path string `json:"path,omitempty"`

	// Remote is the rclone destination, e.g. "gdrive:publix-backups".
	Remote string `json:"remote,omitempty"`
	// RcloneConfig is the contents of an rclone.conf defining the remote.
	// It carries credentials, so it is never sent back to the dashboard.
	RcloneConfig string `json:"rcloneConfig,omitempty"`

	Description string `json:"description,omitempty"`
}

// Redacted returns the target without its credentials.
func (t BackupTarget) Redacted() BackupTarget {
	t.RcloneConfig = ""
	return t
}

// Location is a human description of where the target writes.
func (t BackupTarget) Location() string {
	if t.Type == TargetLocal {
		return t.Path
	}
	return t.Remote
}

// BackupTarget looks up a registered target.
func (s *Settings) BackupTarget(name string) (BackupTarget, bool) {
	for _, t := range s.BackupTargets {
		if t.Name == name {
			return t, true
		}
	}
	return BackupTarget{}, false
}

// BackupTargetNames lists every registered target.
func (s *Settings) BackupTargetNames() []string {
	out := make([]string, 0, len(s.BackupTargets))
	for _, t := range s.BackupTargets {
		out = append(out, t.Name)
	}
	return out
}

var rcloneRemoteRe = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_. -]*:[^\x00'"\n]*$`)

// ValidateBackupTarget checks a target before it is saved.
func (s *Settings) ValidateBackupTarget(t BackupTarget) error {
	var errs []string
	if !volumeNameRe.MatchString(t.Name) {
		errs = append(errs, fmt.Sprintf("name %q must be lowercase alphanumeric with dots, dashes or underscores", t.Name))
	}
	if _, exists := s.BackupTarget(t.Name); exists {
		errs = append(errs, fmt.Sprintf("a backup target named %q already exists", t.Name))
	}
	switch t.Type {
	case TargetLocal:
		path := filepath.Clean(t.Path)
		switch {
		case t.Path == "":
			errs = append(errs, "a host path is required")
		case !filepath.IsAbs(path):
			errs = append(errs, fmt.Sprintf("path %q must be absolute", t.Path))
		case path == "/":
			errs = append(errs, "path \"/\" is the whole host filesystem")
		case strings.ContainsAny(path, ":'\"$`\\"):
			errs = append(errs, fmt.Sprintf("path %q contains a character publix cannot pass to docker safely", path))
		default:
			// The same refusals as for volumes: a backup helper running
			// with /etc mounted writable is as dangerous as a project.
			real := resolve(path)
			for _, forbidden := range s.ProtectedPaths() {
				if within(path, forbidden) || within(real, forbidden) {
					errs = append(errs, fmt.Sprintf("path %s is inside %s, which publix will not write backups into", path, forbidden))
					break
				}
			}
		}
	case TargetRclone:
		if !rcloneRemoteRe.MatchString(t.Remote) {
			errs = append(errs, fmt.Sprintf("remote %q should look like gdrive:publix-backups — a remote name from the config, a colon, and a path", t.Remote))
		}
		if strings.TrimSpace(t.RcloneConfig) == "" {
			errs = append(errs, "an rclone config is required — paste the section for the remote from `rclone config show`")
		} else if remote, _, ok := strings.Cut(t.Remote, ":"); ok && !strings.Contains(t.RcloneConfig, "["+remote+"]") {
			errs = append(errs, fmt.Sprintf("the rclone config has no [%s] section", remote))
		}
	default:
		errs = append(errs, fmt.Sprintf("type %q is not one of local, rclone", t.Type))
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid backup target:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// BackupTargetUsage lists the databases whose schedule writes to a target,
// as "project/database".
func BackupTargetUsage(projects []*Project, target string) []string {
	var out []string
	for _, p := range projects {
		for _, d := range p.Databases {
			if d.Backup != nil && d.Backup.Target == target {
				out = append(out, p.Name+"/"+d.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}
