package store

import (
	"strings"
	"testing"
)

// The first database of a kind gets the name frameworks already read, and
// a second one must not silently take the same variable from it.
func TestDatabasePrefixes(t *testing.T) {
	p := &Project{ID: "p1"}
	add := func(engine DBEngine, name string) *Database {
		t.Helper()
		d, err := p.NewDatabase(name, engine, "", "")
		if err != nil {
			t.Fatal(err)
		}
		p.Databases = append(p.Databases, d)
		return d
	}

	pg := add(EnginePostgres, "")
	if pg.Name != "main" || pg.EnvPrefix != "DATABASE" {
		t.Errorf("first postgres = %s/%s, want main/DATABASE", pg.Name, pg.EnvPrefix)
	}
	if got := add(EngineRedis, "").EnvPrefix; got != "REDIS" {
		t.Errorf("first redis prefix = %s, want REDIS", got)
	}
	if got := add(EngineMongo, "docs").EnvPrefix; got != "MONGODB" {
		t.Errorf("first mongo prefix = %s, want MONGODB", got)
	}
	if got := add(EngineMySQL, "legacy-db").EnvPrefix; got != "LEGACY_DB" {
		t.Errorf("second SQL database prefix = %s, want LEGACY_DB", got)
	}

	env := p.DatabaseEnv()
	for _, k := range []string{"DATABASE_URL", "DATABASE_PASSWORD", "REDIS_URL", "MONGODB_URI", "LEGACY_DB_URL"} {
		if env[k] == "" {
			t.Errorf("%s is not set", k)
		}
	}
	if !strings.HasPrefix(env["DATABASE_URL"], "postgresql://app:"+pg.Password+"@publix-db-p1-main:5432/main") {
		t.Errorf("DATABASE_URL = %s", env["DATABASE_URL"])
	}
	if !strings.HasSuffix(env["MONGODB_URL"], "/docs?authSource=admin") {
		t.Errorf("MONGODB_URL = %s, want authSource=admin", env["MONGODB_URL"])
	}
}

func TestDatabaseValidation(t *testing.T) {
	p := &Project{ID: "p1"}
	d, _ := p.NewDatabase("main", EnginePostgres, "", "")
	p.Databases = append(p.Databases, d)

	if _, err := p.NewDatabase("main", EngineRedis, "", ""); err == nil || !strings.Contains(err.Error(), "already has a database named") {
		t.Errorf("a duplicate name was accepted: %v", err)
	}
	if _, err := p.NewDatabase("other", EngineMySQL, "", "DATABASE"); err == nil || !strings.Contains(err.Error(), "already uses the prefix") {
		t.Errorf("a duplicate prefix was accepted: %v", err)
	}
	if _, err := p.NewDatabase("x", "oracle", "", ""); err == nil {
		t.Error("an unknown engine was accepted")
	}
	if _, err := p.NewDatabase("x", EnginePostgres, "16; rm -rf /", ""); err == nil {
		t.Error("a version that is not an image tag was accepted")
	}
}

// The redacted copy the dashboard receives must never carry a password.
func TestRedactedDropsDatabasePasswords(t *testing.T) {
	p := &Project{ID: "p1"}
	d, _ := p.NewDatabase("", EnginePostgres, "", "")
	p.Databases = []*Database{d}
	if p.Redacted().Databases[0].Password != "" {
		t.Error("the redacted project carries the database password")
	}
	if d.Password == "" {
		t.Error("redacting cleared the stored password")
	}
}

func TestBackupTargetValidation(t *testing.T) {
	s := DefaultSettings()
	cases := []struct {
		target BackupTarget
		want   string
	}{
		{BackupTarget{Name: "disk", Type: TargetLocal, Path: "/etc/backups"}, "inside /etc"},
		{BackupTarget{Name: "disk", Type: TargetLocal, Path: "relative"}, "must be absolute"},
		{BackupTarget{Name: "gd", Type: TargetRclone, Remote: "gdrive:x"}, "rclone config is required"},
		{BackupTarget{Name: "gd", Type: TargetRclone, Remote: "gdrive:x", RcloneConfig: "[other]\ntype = drive"}, "no [gdrive] section"},
		{BackupTarget{Name: "gd", Type: TargetRclone, Remote: "nocolon", RcloneConfig: "[x]"}, "should look like"},
		{BackupTarget{Name: "x", Type: "ftp"}, "not one of local, rclone"},
	}
	for _, c := range cases {
		err := s.ValidateBackupTarget(c.target)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want an error mentioning %q", c.target, err, c.want)
		}
	}
	ok := BackupTarget{Name: "gd", Type: TargetRclone, Remote: "gdrive:publix", RcloneConfig: "[gdrive]\ntype = drive\n"}
	if err := s.ValidateBackupTarget(ok); err != nil {
		t.Errorf("a valid rclone target was refused: %v", err)
	}
	if ok.Redacted().RcloneConfig != "" {
		t.Error("Redacted kept the rclone config")
	}
}
