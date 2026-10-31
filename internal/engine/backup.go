package engine

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

// Backups run entirely in throwaway containers.
//
// publix never reads a database's files or a dump itself. The dump is taken
// by the database's own image — so pg_dump always matches the server's
// major version — into a staging volume, and shipped from there by a
// second container that has the target mounted or rclone configured. That
// is what makes a backup work the same whether publix runs on the host or
// in a container, and whether the target is a disk or Google Drive.

// stagingVolume holds dumps between being taken and being shipped.
const stagingVolume = "publix-backup-staging"

// BackupFile is one backup present on a target.
type BackupFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// backupDir is where a database's backups live on a target. It is keyed by
// the project ID and the database name: the ID survives a project rename,
// and the name survives a database being deleted and created again, so the
// new one can be restored from the old one's backups.
func backupDir(p *store.Project, d *store.Database) string {
	return p.ID + "/" + d.Name
}

// backupExt is the file extension of an engine's dump format.
func backupExt(e store.DBEngine) string {
	switch e {
	case store.EnginePostgres:
		return ".dump"
	case store.EngineMySQL:
		return ".sql"
	case store.EngineMongo:
		return ".archive.gz"
	case store.EngineRedis:
		return ".rdb"
	}
	return ".tar.gz"
}

// dbLock serialises backups and restores of one database. Two at once
// would race on the staging directory and, for a restore, on the data.
func (e *Engine) dbLock(d *store.Database) func() {
	l := e.projectLock("db:" + d.ID)
	l.Lock()
	return l.Unlock
}

// Backup takes a backup of a database and writes it to a target.
func (e *Engine) Backup(ctx context.Context, projectID, dbName, targetName, trigger string) (*store.BackupRecord, error) {
	p, d, target, err := e.backupSubjects(projectID, dbName, targetName)
	if err != nil {
		return nil, err
	}
	unlock := e.dbLock(d)
	defer unlock()

	now := time.Now().UTC()
	rec := store.BackupRecord{
		ID:        store.NewID(),
		Kind:      "backup",
		Trigger:   trigger,
		Target:    target.Name,
		File:      fmt.Sprintf("%s-%s-%s%s", p.Slug, d.Name, now.Format("20060102-150405"), backupExt(d.Engine)),
		Status:    store.BackupRunning,
		StartedAt: now,
	}
	e.recordBackup(p.ID, d.ID, rec)

	size, err := e.backup(ctx, p, d, target, rec)
	rec.Size = size
	return e.finishBackup(p.ID, d.ID, rec, err)
}

func (e *Engine) backup(ctx context.Context, p *store.Project, d *store.Database, target store.BackupTarget, rec store.BackupRecord) (int64, error) {
	if err := e.prepareStaging(ctx, p, d); err != nil {
		return 0, err
	}
	stage := "/staging/" + rec.ID
	defer e.cleanStaging(ctx, p, d, rec.ID)

	if d.Engine.HasServer() {
		if err := e.waitDatabaseReady(ctx, p, d, 30*time.Second); err != nil {
			return 0, err
		}
	}

	image, script, env, binds := dumpStep(p, d, stage+"/"+rec.File)
	binds = append(binds, stagingVolume+":/staging")
	if _, err := e.runHelper(ctx, p, d, image, []string{"sh", "-c", "set -e; mkdir -p " + stage + "; " + script}, env, binds); err != nil {
		return 0, fmt.Errorf("taking the dump: %w", err)
	}

	keep := 0
	if d.Backup != nil && d.Backup.Target == target.Name {
		keep = d.Backup.Keep
	}
	out, err := e.onTarget(ctx, p, d, target, shipScript(target, backupDir(p, d), stage, rec.File, keep), true)
	if err != nil {
		return 0, fmt.Errorf("writing to %s: %w", target.Name, err)
	}
	return parseSize(out), nil
}

// dumpStep is the container that writes a dump of d to file.
func dumpStep(p *store.Project, d *store.Database, file string) (image, script string, env, binds []string) {
	host := d.Host(p.ID)
	switch d.Engine {
	case store.EnginePostgres:
		// The custom format is compressed, and pg_restore can restore it
		// selectively, which a plain SQL file cannot offer.
		return d.Image(), fmt.Sprintf("pg_dump -h %s -U %s -d %s -Fc -f %s", host, d.User, d.DBName, file),
			[]string{"PGPASSWORD=" + d.Password}, nil
	case store.EngineMySQL:
		return d.Image(), fmt.Sprintf("mysqldump -h %s -uroot --single-transaction --routines --triggers --events --databases %s > %s", host, d.DBName, file),
			[]string{"MYSQL_PWD=" + d.Password}, nil
	case store.EngineMongo:
		return d.Image(), fmt.Sprintf(`mongodump --quiet --uri="$MONGO_URL" --archive=%s --gzip`, file),
			[]string{"MONGO_URL=" + d.URL(p.ID)}, nil
	case store.EngineRedis:
		return d.Image(), fmt.Sprintf("redis-cli -h %s --no-auth-warning --rdb %s >/dev/null", host, file),
			[]string{"REDISCLI_AUTH=" + d.Password}, nil
	}
	// SQLite is files. Archiving them includes the -wal and -shm files, so
	// a database in WAL mode is captured consistently enough to open.
	return helperImage, "tar czf " + file + " -C /db .", nil, []string{d.VolumeName(p.ID) + ":/db:ro"}
}

// shipScript copies a staged dump onto a target and prunes old backups.
func shipScript(t store.BackupTarget, dir, stage, file string, keep int) string {
	prune := ""
	if t.Type == store.TargetLocal {
		dest := "/target/" + dir
		if keep > 0 {
			// File names sort chronologically, so the newest are first
			// in reverse order and everything past keep goes.
			prune = fmt.Sprintf(`; ls -1 %s | sort -r | tail -n +%d | while read f; do rm -f -- "%s/$f"; done`, dest, keep+1, dest)
		}
		return fmt.Sprintf(`mkdir -p %[1]s && cp %[2]s/%[3]s %[1]s/%[3]s && echo "SIZE=$(wc -c < %[1]s/%[3]s)"%[4]s`,
			dest, stage, file, prune)
	}
	dest := rclonePath(t.Remote, dir)
	if keep > 0 {
		prune = fmt.Sprintf(`; rclone lsf --files-only '%[1]s' | sort -r | tail -n +%[2]d | while read f; do rclone deletefile "%[1]s/$f"; done`, dest, keep+1)
	}
	return fmt.Sprintf(`rclone copyto %[2]s/%[3]s '%[1]s/%[3]s' && echo "SIZE=$(wc -c < %[2]s/%[3]s)"%[4]s`,
		dest, stage, file, prune)
}

// rclonePath joins a remote and a directory under it.
func rclonePath(remote, dir string) string {
	if strings.HasSuffix(remote, ":") || strings.HasSuffix(remote, "/") {
		return remote + dir
	}
	return remote + "/" + dir
}

// onTarget runs a script with the target reachable: a local target is
// mounted at /target, an rclone one is configured. withStaging also mounts
// the staging volume at /staging.
func (e *Engine) onTarget(ctx context.Context, p *store.Project, d *store.Database, t store.BackupTarget, script string, withStaging bool) (string, error) {
	var binds []string
	if withStaging {
		binds = append(binds, stagingVolume+":/staging")
	}
	if t.Type == store.TargetLocal {
		binds = append(binds, t.Path+":/target")
		return e.runHelper(ctx, p, d, helperImage, []string{"sh", "-c", "set -e; " + script}, nil, binds)
	}
	// The config goes in through the environment rather than a file:
	// publix may be in a container with no path the helper could share,
	// and the environment never reaches the log the way a command does.
	prelude := `printf '%s' "$PUBLIX_RCLONE_CONF" > /tmp/rclone.conf; export RCLONE_CONFIG=/tmp/rclone.conf; `
	return e.runHelper(ctx, p, d, rcloneImage, []string{"sh", "-c", "set -e; " + prelude + script},
		[]string{"PUBLIX_RCLONE_CONF=" + t.RcloneConfig}, binds)
}

// Restore replaces a database's contents with a backup from a target.
func (e *Engine) Restore(ctx context.Context, projectID, dbName, targetName, file string) (*store.BackupRecord, error) {
	p, d, target, err := e.backupSubjects(projectID, dbName, targetName)
	if err != nil {
		return nil, err
	}
	if file == "" || strings.ContainsAny(file, "/\\'\"$`") || strings.HasPrefix(file, ".") {
		return nil, fmt.Errorf("%q is not a backup file name — use one list_backups reports", file)
	}
	if !strings.HasSuffix(file, backupExt(d.Engine)) {
		return nil, fmt.Errorf("%s is not a %s backup (expected a %s file)", file, d.Engine, backupExt(d.Engine))
	}
	unlock := e.dbLock(d)
	defer unlock()

	rec := store.BackupRecord{
		ID:        store.NewID(),
		Kind:      "restore",
		Trigger:   "manual",
		Target:    target.Name,
		File:      file,
		Status:    store.BackupRunning,
		StartedAt: time.Now().UTC(),
	}
	e.recordBackup(p.ID, d.ID, rec)
	return e.finishBackup(p.ID, d.ID, rec, e.restore(ctx, p, d, target, rec))
}

func (e *Engine) restore(ctx context.Context, p *store.Project, d *store.Database, target store.BackupTarget, rec store.BackupRecord) error {
	if err := e.prepareStaging(ctx, p, d); err != nil {
		return err
	}
	stage := "/staging/" + rec.ID
	defer e.cleanStaging(ctx, p, d, rec.ID)

	var fetch string
	if target.Type == store.TargetLocal {
		fetch = fmt.Sprintf("mkdir -p %[1]s && cp /target/%[2]s/%[3]s %[1]s/%[3]s", stage, backupDir(p, d), rec.File)
	} else {
		fetch = fmt.Sprintf("mkdir -p %[1]s && rclone copyto '%[2]s/%[3]s' %[1]s/%[3]s", stage, rclonePath(target.Remote, backupDir(p, d)), rec.File)
	}
	if _, err := e.onTarget(ctx, p, d, target, fetch, true); err != nil {
		return fmt.Errorf("fetching %s from %s: %w", rec.File, target.Name, err)
	}

	file := stage + "/" + rec.File
	staging := stagingVolume + ":/staging"
	host := d.Host(p.ID)

	switch d.Engine {
	case store.EnginePostgres, store.EngineMySQL, store.EngineMongo:
		if err := e.waitDatabaseReady(ctx, p, d, 30*time.Second); err != nil {
			return err
		}
		var script string
		var env []string
		switch d.Engine {
		case store.EnginePostgres:
			script = fmt.Sprintf("pg_restore -h %s -U %s -d %s --clean --if-exists --no-owner --no-acl %s", host, d.User, d.DBName, file)
			env = []string{"PGPASSWORD=" + d.Password}
		case store.EngineMySQL:
			script = fmt.Sprintf("mysql -h %s -uroot < %s", host, file)
			env = []string{"MYSQL_PWD=" + d.Password}
		default:
			// The URI names no database: the archive already holds only
			// this one's namespaces, and mongorestore treats a database in
			// the URI as the deprecated --db, which an archive refuses.
			script = fmt.Sprintf(`mongorestore --quiet --uri="$MONGO_URL" --archive=%s --gzip --drop`, file)
			env = []string{fmt.Sprintf("MONGO_URL=mongodb://%s:%s@%s:%d/?authSource=admin", d.User, d.Password, host, d.Engine.Port())}
		}
		_, err := e.runHelper(ctx, p, d, d.Image(), []string{"sh", "-c", "set -e; " + script}, env, []string{staging})
		return err

	case store.EngineRedis:
		// Redis only reads its data at startup, so the server has to be
		// stopped while the files are swapped. With appendonly on it
		// would also ignore the restored snapshot in favour of the old
		// AOF, so the snapshot is loaded once with AOF off and the AOF is
		// then rebuilt from what was loaded.
		name := d.ContainerName(p.ID)
		if err := e.docker.StopContainer(ctx, name, 30*time.Second); err != nil {
			return err
		}
		script := fmt.Sprintf(`rm -rf /data/appendonlydir /data/*.aof; cp %s /data/dump.rdb
redis-server --dir /data --appendonly no --port 6390 --daemonize yes
until redis-cli -p 6390 ping 2>/dev/null | grep -q PONG; do sleep 1; done
redis-cli -p 6390 config set appendonly yes >/dev/null
sleep 1
until redis-cli -p 6390 info persistence | grep -q 'aof_rewrite_in_progress:0' && redis-cli -p 6390 info persistence | grep -q 'aof_rewrite_scheduled:0'; do sleep 1; done
redis-cli -p 6390 shutdown nosave >/dev/null || true`, file)
		_, err := e.runHelper(ctx, p, d, d.Image(), []string{"sh", "-c", "set -e; " + script}, nil,
			[]string{staging, d.VolumeName(p.ID) + ":/data"})
		// Start it again whatever happened: a stopped database is an
		// outage, a failed restore with the old data is not.
		if startErr := e.ProvisionDatabase(context.WithoutCancel(ctx), p, d, nil); startErr != nil && err == nil {
			err = startErr
		}
		return err
	}

	// SQLite: swap the files. A running app keeps the old file open until
	// it reopens it, so the caller is told to redeploy.
	_, err := e.runHelper(ctx, p, d, helperImage, []string{"sh", "-c",
		fmt.Sprintf("set -e; find /db -mindepth 1 -delete; tar xzf %s -C /db; chmod -R a+rwX /db", file),
	}, nil, []string{staging, d.VolumeName(p.ID) + ":/db"})
	return err
}

// ListBackups lists the backups of a database present on a target.
func (e *Engine) ListBackups(ctx context.Context, projectID, dbName, targetName string) ([]BackupFile, error) {
	p, d, target, err := e.backupSubjects(projectID, dbName, targetName)
	if err != nil {
		return nil, err
	}
	var script string
	if target.Type == store.TargetLocal {
		dir := "/target/" + backupDir(p, d)
		script = fmt.Sprintf(`[ -d %[1]s ] || exit 0; for f in %[1]s/*; do [ -f "$f" ] && echo "$(basename "$f")|$(stat -c %%s "$f")|$(stat -c %%Y "$f")"; done; true`, dir)
	} else {
		// Exit status 3 is rclone's "directory not found": a database with
		// no backups yet, not a failure.
		script = fmt.Sprintf(`set +e; rclone lsf --files-only --format pst --separator '|' --time-format unix '%s'; rc=$?; [ $rc -eq 3 ] && exit 0; exit $rc`,
			rclonePath(target.Remote, backupDir(p, d)))
	}
	out, err := e.onTarget(ctx, p, d, target, script, false)
	if err != nil {
		return nil, err
	}

	var files []BackupFile
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 3 || !strings.HasSuffix(parts[0], backupExt(d.Engine)) {
			continue
		}
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		f := BackupFile{Name: parts[0], Size: size}
		if sec, err := strconv.ParseInt(strings.SplitN(parts[2], ".", 2)[0], 10, 64); err == nil {
			f.Modified = time.Unix(sec, 0).UTC()
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return files, nil
}

// TestBackupTarget proves a target can be written to, by writing and
// removing a file — so a wrong rclone config fails now, not at 3am on the
// first scheduled backup.
func (e *Engine) TestBackupTarget(ctx context.Context, t store.BackupTarget) error {
	var script string
	if t.Type == store.TargetLocal {
		script = "touch /target/.publix-write-test && rm /target/.publix-write-test"
	} else {
		probe := rclonePath(t.Remote, ".publix-write-test")
		script = fmt.Sprintf("echo ok > /tmp/probe && rclone copyto /tmp/probe '%[1]s' && rclone deletefile '%[1]s'", probe)
	}
	_, err := e.onTarget(ctx, &store.Project{ID: "test"}, &store.Database{ID: "test"}, t, script, false)
	return err
}

// backupSubjects resolves the project, database and target of a request.
func (e *Engine) backupSubjects(projectID, dbName, targetName string) (*store.Project, *store.Database, store.BackupTarget, error) {
	p, ok := e.store.Project(projectID)
	if !ok {
		return nil, nil, store.BackupTarget{}, &store.NotFoundError{Kind: "project", ID: projectID}
	}
	d, ok := p.Database(dbName)
	if !ok {
		return nil, nil, store.BackupTarget{}, &store.NotFoundError{Kind: "database", ID: dbName}
	}
	set := e.store.Settings()
	if targetName == "" && d.Backup != nil {
		targetName = d.Backup.Target
	}
	if targetName == "" && len(set.BackupTargets) == 1 {
		targetName = set.BackupTargets[0].Name
	}
	if targetName == "" {
		if len(set.BackupTargets) == 0 {
			return nil, nil, store.BackupTarget{}, fmt.Errorf("no backup target is registered; add one under Settings → Backups")
		}
		return nil, nil, store.BackupTarget{}, fmt.Errorf("say which backup target to use: %s", strings.Join(set.BackupTargetNames(), ", "))
	}
	t, ok := set.BackupTarget(targetName)
	if !ok {
		return nil, nil, store.BackupTarget{}, fmt.Errorf("no backup target named %q; registered: %s", targetName, strings.Join(set.BackupTargetNames(), ", "))
	}
	return p, d, t, nil
}

func (e *Engine) prepareStaging(ctx context.Context, p *store.Project, d *store.Database) error {
	return e.docker.EnsureVolume(ctx, stagingVolume, map[string]string{"publix.managed": "true"})
}

// cleanStaging removes a run's staging directory on every path, so a
// failing schedule cannot fill the disk with half-shipped dumps.
func (e *Engine) cleanStaging(ctx context.Context, p *store.Project, d *store.Database, id string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	_, _ = e.runHelper(cleanup, p, d, helperImage, []string{"rm", "-rf", "/staging/" + id}, nil, []string{stagingVolume + ":/staging"})
}

func (e *Engine) recordBackup(projectID, dbID string, rec store.BackupRecord) {
	_, _ = e.store.UpdateProject(projectID, func(p *store.Project) error {
		if d, ok := p.Database(dbID); ok {
			if existing := d.BackupRecord(rec.ID); existing != nil {
				*existing = rec
			} else {
				d.AddBackupRecord(rec)
			}
		}
		return nil
	})
}

func (e *Engine) finishBackup(projectID, dbID string, rec store.BackupRecord, err error) (*store.BackupRecord, error) {
	now := time.Now().UTC()
	rec.FinishedAt = &now
	rec.Status = store.BackupSucceeded
	if err != nil {
		rec.Status = store.BackupFailed
		rec.Error = err.Error()
	}
	e.recordBackup(projectID, dbID, rec)
	return &rec, err
}

// runHelper runs a one-shot container to completion and returns its output.
// A non-zero exit is an error carrying the tail of that output, which is
// where pg_dump or rclone explains itself.
func (e *Engine) runHelper(ctx context.Context, p *store.Project, d *store.Database, image string, cmd, env, binds []string) (string, error) {
	if err := e.ensureImage(ctx, image, nil); err != nil {
		return "", err
	}
	set := e.store.Settings()
	cfg := &dockerapi.CreateConfig{
		Image:      image,
		Entrypoint: cmd[:1],
		Cmd:        cmd[1:],
		Env:        env,
		Labels:     dbLabels(p, d, RoleHelper),
		HostConfig: &dockerapi.HostConfig{
			NetworkMode:   set.Network,
			Binds:         binds,
			RestartPolicy: &dockerapi.RestartPolicy{Name: "no"},
		},
	}
	name := fmt.Sprintf("publix-db-helper-%s-%s", p.ID, store.NewID())
	created, err := e.docker.CreateContainer(ctx, name, cfg)
	if err != nil {
		return "", fmt.Errorf("creating a helper container: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = e.docker.RemoveContainer(cleanup, created.ID, true, false)
	}()
	if err := e.docker.StartContainer(ctx, created.ID); err != nil {
		return "", fmt.Errorf("starting a helper container: %w", err)
	}
	code, err := e.docker.WaitContainer(ctx, created.ID)
	var out bytes.Buffer
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = e.docker.ContainerLogs(logCtx, created.ID, dockerapi.LogOptions{Stdout: true, Stderr: true}, &out)
	if err != nil {
		return out.String(), err
	}
	if code != 0 {
		return out.String(), fmt.Errorf("exited with code %d: %s", code, lastLines(out.String(), 8))
	}
	return out.String(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.TrimSpace(strings.Join(lines, " | "))
	if out == "" {
		return "no output"
	}
	return out
}

func parseSize(out string) int64 {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SIZE="); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}
