package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/store"
)

// dbHarness adds a database to a fresh project and a local backup target,
// and removes the database's data volume when the test ends — the teardown
// deliberately keeps it, and a test must not leak it.
func dbHarness(t *testing.T, engine store.DBEngine) (*harness, *store.Project, *store.Database) {
	h := newHarness(t)
	p := h.project("dbtest", nil)
	d, err := p.NewDatabase("", engine, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, err := h.engine.AddDatabase(ctx, p.ID, d, nil); err != nil {
		t.Fatalf("creating the %s database: %v", engine, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = h.engine.RemoveDatabase(ctx, p, d, true)
	})

	target := store.BackupTarget{Name: "disk", Type: store.TargetLocal, Path: t.TempDir()}
	if err := h.store.SetSettings(func(s *store.Settings) error {
		s.BackupTargets = append(s.BackupTargets, target)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.TestBackupTarget(ctx, target); err != nil {
		t.Fatalf("testing the target: %v", err)
	}
	p, _ = h.store.Project(p.ID)
	return h, p, d
}

// execIn runs a shell command in the database container and returns stdout.
func execIn(t *testing.T, h *harness, p *store.Project, d *store.Database, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := h.docker.Exec(ctx, d.ContainerName(p.ID), []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("%s: exit %d: %s %s", script, res.ExitCode, res.Stdout, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout)
}

// Every engine must survive the trip: write a value, back up, destroy the
// value, restore, and find the value again.
func TestDatabaseBackupRestoreRoundTrip(t *testing.T) {
	type scripts struct{ write, destroy, read string }
	cases := map[store.DBEngine]func(d *store.Database) scripts{
		store.EnginePostgres: func(d *store.Database) scripts {
			psql := "psql -U app -d main -tAc "
			return scripts{
				psql + `"create table t (v text); insert into t values ('kept')"`,
				psql + `"drop table t"`,
				psql + `"select v from t"`,
			}
		},
		store.EngineMySQL: func(d *store.Database) scripts {
			my := "mysql -uroot -p" + d.Password + " main -N -e "
			return scripts{
				my + `"create table t (v text); insert into t values ('kept')"`,
				my + `"drop table t"`,
				my + `"select v from t" 2>/dev/null`,
			}
		},
		store.EngineMongo: func(d *store.Database) scripts {
			sh := "mongosh --quiet -u app -p " + d.Password + " --authenticationDatabase admin main --eval "
			return scripts{
				sh + `'db.t.insertOne({v: "kept"})'`,
				sh + `'db.t.drop()'`,
				sh + `'print(db.t.findOne().v)'`,
			}
		},
		store.EngineRedis: func(d *store.Database) scripts {
			cli := "redis-cli --no-auth-warning -a " + d.Password + " "
			return scripts{cli + "set k kept", cli + "del k", cli + "get k"}
		},
	}

	for engine, mk := range cases {
		t.Run(string(engine), func(t *testing.T) {
			h, p, d := dbHarness(t, engine)
			s := mk(d)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			execIn(t, h, p, d, s.write)
			rec, err := h.engine.Backup(ctx, p.ID, d.Name, "", "test")
			if err != nil {
				t.Fatalf("backup: %v", err)
			}
			if rec.Size == 0 {
				t.Error("the backup recorded no size")
			}
			files, err := h.engine.ListBackups(ctx, p.ID, d.Name, "")
			if err != nil || len(files) != 1 || files[0].Name != rec.File {
				t.Fatalf("listing = %+v, %v; want just %s", files, err, rec.File)
			}

			execIn(t, h, p, d, s.destroy)
			if _, err := h.engine.Restore(ctx, p.ID, d.Name, "", rec.File); err != nil {
				t.Fatalf("restore: %v", err)
			}
			if got := execIn(t, h, p, d, s.read); got != "kept" {
				t.Errorf("after restore read %q, want kept", got)
			}

			p, _ = h.store.Project(p.ID)
			nd, _ := p.Database(d.Name)
			if len(nd.Backups) != 2 || nd.Backups[0].Kind != "restore" || nd.Backups[0].Status != store.BackupSucceeded {
				t.Errorf("history = %+v", nd.Backups)
			}
		})
	}
}

// Retention must keep exactly the newest backups and nothing else.
func TestBackupRetention(t *testing.T) {
	h, p, d := dbHarness(t, store.EngineRedis)
	if _, err := h.store.UpdateProject(p.ID, func(p *store.Project) error {
		db, _ := p.Database(d.Name)
		db.Backup = &store.BackupPolicy{Schedule: "@daily", Target: "disk", Keep: 2}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var names []string
	for i := 0; i < 3; i++ {
		rec, err := h.engine.Backup(ctx, p.ID, d.Name, "", "test")
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, rec.File)
		time.Sleep(1100 * time.Millisecond) // file names carry the second
	}
	files, err := h.engine.ListBackups(ctx, p.ID, d.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != names[2] || files[1].Name != names[1] {
		t.Errorf("kept %+v, want %s and %s", files, names[2], names[1])
	}
}

// A database declared in deployment.yaml is created before the build and
// its variables reach the app — here an SQLite one, which also proves the
// directory is mounted and writable by the app.
func TestDeclaredDatabaseReachesTheApp(t *testing.T) {
	h := newHarness(t)
	p := h.project("withdb", map[string]string{
		"deployment.yaml": "port: 80\ndatabases:\n  - sqlite\n",
		"Dockerfile": "FROM nginx:alpine\n" +
			`CMD ["sh", "-c", "touch \"$DATABASE_PATH\" && echo \"$DATABASE_URL\" > /usr/share/nginx/html/index.html && exec nginx -g 'daemon off;'"]` + "\n",
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if p, ok := h.store.Project(p.ID); ok {
			// The app mounts the volume, so it has to go first.
			_ = h.engine.Teardown(ctx, p)
			for _, d := range p.Databases {
				_ = h.engine.RemoveDatabase(ctx, p, d, true)
			}
		}
	})

	h.mustSucceed(h.deploy(p, Options{Trigger: "test"}))

	p, _ = h.store.Project(p.ID)
	if len(p.Databases) != 1 || !p.Databases[0].Declared {
		t.Fatalf("databases = %+v, want one declared", p.Databases)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if body := httpGet(t, h, h.oneContainer(ctx, p.ID), 80, "/"); !strings.Contains(body, "file:/data/main/main.db") {
		t.Errorf("the app saw DATABASE_URL = %q", body)
	}
}

// A SQLite database an app has open in WAL mode keeps its newest writes in
// the -wal file until a checkpoint. The backup has to carry them, and come
// out as a database that opens on its own.
func TestSQLiteBackupCarriesTheWAL(t *testing.T) {
	h, p, d := dbHarness(t, store.EngineSQLite)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	vol := []string{d.VolumeName(p.ID) + ":/db"}
	py := func(code string) string {
		t.Helper()
		out, err := h.engine.runHelper(ctx, p, d, sqliteSnapshotImage, []string{"python3", "-c", code}, nil, vol)
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(out)
	}

	// os._exit skips the close that would checkpoint, leaving the row only
	// in the -wal file — what a crashed or still-running app leaves behind.
	py(`import sqlite3, os
c = sqlite3.connect("/db/main.db")
c.execute("pragma journal_mode=wal")
c.execute("pragma wal_autocheckpoint=0")
c.execute("create table t (v text)")
c.execute("insert into t values ('kept')")
c.commit()
os._exit(0)`)
	if got := py(`import os; print(os.path.getsize("/db/main.db-wal") > 0)`); got != "True" {
		t.Fatalf("the test did not leave a -wal file behind (%s)", got)
	}

	rec, err := h.engine.Backup(ctx, p.ID, d.Name, "", "test")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	py(`import os
for f in os.listdir("/db"): os.remove("/db/" + f)`)
	if _, err := h.engine.Restore(ctx, p.ID, d.Name, "", rec.File); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := py(`import os, sqlite3
print(sorted(os.listdir("/db")), sqlite3.connect("/db/main.db").execute("select v from t").fetchone()[0])`)
	if got != "['main.db'] kept" {
		t.Errorf("after restore: %s, want only main.db holding the row", got)
	}
}

// The explorer has to read every engine the same way a person would: list
// what is there, page through one object, and — for SQL — answer a query
// while refusing to write.
func TestExploreEveryEngine(t *testing.T) {
	type seed struct {
		script string // run in the database container
		object string // what the overview must list
		want   string // a value the page must contain
		query  string // a read-only query, "" for engines without one
	}
	cases := map[store.DBEngine]func(d *store.Database) seed{
		store.EnginePostgres: func(d *store.Database) seed {
			return seed{
				`psql -U app -d main -c "create table people (id int, name text, tags jsonb); insert into people values (1, 'ada', '{\"a\":1}'), (2, 'grace', null)"`,
				"people", `"grace"`, "SELECT name FROM people WHERE id = 1",
			}
		},
		store.EngineMySQL: func(d *store.Database) seed {
			return seed{
				"mysql -uroot -p" + d.Password + ` main -e "create table people (id int, name text); insert into people values (1, 'ada'), (2, 'grace\tline')"`,
				"people", `"grace\tline"`, "SELECT name FROM people WHERE id = 1",
			}
		},
		store.EngineMongo: func(d *store.Database) seed {
			return seed{
				"mongosh --quiet -u app -p " + d.Password + ` --authenticationDatabase admin main --eval 'db.people.insertMany([{name: "ada"}, {name: "grace", tags: ["x"]}])'`,
				"people", `"grace"`, "",
			}
		},
		store.EngineRedis: func(d *store.Database) seed {
			return seed{
				"redis-cli --no-auth-warning -a " + d.Password + " hset people:1 name grace role admin",
				"people:1", `"grace"`, "",
			}
		},
	}

	for engine, mk := range cases {
		t.Run(string(engine), func(t *testing.T) {
			h, p, d := dbHarness(t, engine)
			s := mk(d)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			execIn(t, h, p, d, s.script)
			checkExplorer(t, h.engine, ctx, p, d, s.object, s.want, s.query)
		})
	}

	t.Run("sqlite", func(t *testing.T) {
		h, p, d := dbHarness(t, store.EngineSQLite)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if out, err := h.engine.runHelper(ctx, p, d, sqliteSnapshotImage, []string{"python3", "-c", `import sqlite3
c = sqlite3.connect("/db/main.db")
c.execute("create table people (id int, name text, photo blob)")
c.execute("insert into people values (1, 'ada', x'00ff'), (2, 'grace', null)")
c.commit()`}, nil, []string{d.VolumeName(p.ID) + ":/db"}); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		checkExplorer(t, h.engine, ctx, p, d, "people", `"grace"`, "SELECT name FROM people WHERE id = 1")
	})
}

func checkExplorer(t *testing.T, e *Engine, ctx context.Context, p *store.Project, d *store.Database, object, want, query string) {
	t.Helper()
	ov, err := e.ExploreDatabase(ctx, p, d, "")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	found := false
	for _, o := range ov.Objects {
		found = found || o.Name == object
	}
	if !found {
		t.Fatalf("overview %+v does not list %s", ov.Objects, object)
	}
	if ov.Queryable != (query != "") {
		t.Errorf("queryable = %v", ov.Queryable)
	}

	page, err := e.BrowseDatabase(ctx, p, d, object, "", 0, 1)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(page.Rows) != 1 || len(page.Columns) == 0 {
		t.Fatalf("browse with limit 1 = %d rows, columns %v", len(page.Rows), page.Columns)
	}
	if d.Engine != store.EngineRedis && (!page.More || page.Total == nil || *page.Total != 2) {
		t.Errorf("page should say more rows follow out of 2: more=%v total=%v", page.More, page.Total)
	}
	all, err := e.BrowseDatabase(ctx, p, d, object, "", 0, 10)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if !strings.Contains(fmt.Sprint(rawRows(all)), want) {
		t.Errorf("browse rows %v do not contain %s", rawRows(all), want)
	}
	if _, err := e.BrowseDatabase(ctx, p, d, "no_such_thing", "", 0, 10); err == nil && d.Engine != store.EngineMongo {
		t.Error("browsing an object that does not exist succeeded")
	}

	if query == "" {
		if _, err := e.QueryDatabase(ctx, p, d, "SELECT 1", 10); err == nil {
			t.Error("a query against an engine without SQL succeeded")
		}
		return
	}
	res, err := e.QueryDatabase(ctx, p, d, query+";", 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Rows) != 1 || string(res.Rows[0][0]) != `"ada"` {
		t.Errorf("query rows = %v", rawRows(res))
	}
	if _, err := e.QueryDatabase(ctx, p, d, "DELETE FROM people", 10); err == nil {
		t.Error("a DELETE got through the read-only query")
	}
	if _, err := e.QueryDatabase(ctx, p, d, "SELECT 1; DROP TABLE people", 10); err == nil {
		t.Error("a second statement got through")
	}
	// The wrapping above already makes a DELETE a syntax error. Underneath
	// it, the session itself has to refuse a write.
	var direct error
	switch d.Engine {
	case store.EnginePostgres:
		_, direct = e.psql(ctx, p, d, "DELETE FROM people")
	case store.EngineMySQL:
		_, direct = e.mysql(ctx, p, d, false, "DELETE FROM people")
	}
	if d.Engine != store.EngineSQLite && (direct == nil || !strings.Contains(strings.ToLower(direct.Error()), "read")) {
		t.Errorf("a bare DELETE in the explorer's session was not refused as read-only: %v", direct)
	}
	if after, err := e.BrowseDatabase(ctx, p, d, object, "", 0, 10); err != nil || len(after.Rows) != 2 {
		t.Errorf("the table changed after refused writes: %v %v", rawRows(after), err)
	}
}

func rawRows(r *ExploreResult) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, row := range r.Rows {
		for _, c := range row {
			out = append(out, string(c))
		}
	}
	return out
}
