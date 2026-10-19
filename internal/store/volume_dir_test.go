package store

import (
	"os"
	"path/filepath"
	"testing"
)

// A read-only mount must leave its directory exactly as it is: no chmod (which
// a root-squashed NFS export refuses), and no creating it either.
func TestExistingDirLeavesPermissionsAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := Volume{Name: "music", Path: dir, Scope: ScopeShared}

	got, err := v.ExistingDir("p1")
	if err != nil {
		t.Fatalf("ExistingDir: %v", err)
	}
	if got != dir {
		t.Fatalf("ExistingDir = %q, want %q", got, dir)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o755 {
		t.Fatalf("mode changed to %o, want 755", perm)
	}
}

func TestExistingDirDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	v := Volume{Name: "data", Path: root, Scope: ScopeProject}

	if _, err := v.ExistingDir("missing"); err == nil {
		t.Fatal("ExistingDir succeeded for a directory that does not exist")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("ExistingDir created the directory (stat err = %v)", err)
	}
}
