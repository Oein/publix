package engine

import (
	"context"
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
