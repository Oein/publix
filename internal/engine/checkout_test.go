package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/store"
)

// discardLog returns a build log the fetch helpers can write to.
func discardLog(t *testing.T) *buildlog.Log {
	t.Helper()
	store, err := buildlog.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l, err := store.Create("test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	return l
}

// makeRepo builds a local git repository with the given commits and returns
// its path plus the SHA of each commit in order.
func makeRepo(t *testing.T, commits []map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "-q", "-b", "main")
	var shas []string
	for i, files := range commits {
		for name, content := range files {
			p := filepath.Join(dir, name)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-q", "-m", "commit "+string(rune('a'+i)))
		shas = append(shas, run("rev-parse", "HEAD"))
	}
	return dir, shas
}

// The checkout has to handle a branch, an exact commit and a tag, because a
// deploy uses the first, a rollback the second, and people use the third.
func TestFetchResolvesBranchCommitAndTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repo, shas := makeRepo(t, []map[string]string{
		{"file.txt": "one"},
		{"file.txt": "two"},
		{"file.txt": "three"},
	})
	tagCmd := exec.Command("git", "tag", "v1.0.0", shas[1])
	tagCmd.Dir = repo
	if out, err := tagCmd.CombinedOutput(); err != nil {
		t.Fatalf("tagging: %v\n%s", err, out)
	}

	home := t.TempDir()
	t.Setenv("PUBLIX_HOME", home)
	st, err := store.OpenAt(filepath.Join(home, "publix.json"))
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: st}

	cases := []struct {
		name string
		ref  string
		want string
	}{
		{"branch", "main", "three"},
		{"exact commit", shas[0], "one"},
		{"middle commit", shas[1], "two"},
		{"tag", "v1.0.0", "two"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := filepath.Join(t.TempDir(), "checkout")
			dc := &Context{Log: discardLog(t)}

			if err := e.fetch(context.Background(), dc, work, "file://"+repo, tc.ref); err != nil {
				t.Fatalf("fetch %s: %v", tc.ref, err)
			}
			got, err := os.ReadFile(filepath.Join(work, "file.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("checked out %q, want %q", got, tc.want)
			}
		})
	}
}

// A checkout is reused between deploys, so moving it from one commit to
// another — forwards and backwards — has to work repeatedly in place.
func TestFetchReusesCheckoutAcrossRefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	repo, shas := makeRepo(t, []map[string]string{
		{"file.txt": "one"},
		{"file.txt": "two"},
	})

	home := t.TempDir()
	t.Setenv("PUBLIX_HOME", home)
	st, _ := store.OpenAt(filepath.Join(home, "publix.json"))
	e := &Engine{store: st}

	work := filepath.Join(t.TempDir(), "checkout")
	dc := &Context{Log: discardLog(t)}

	// Deploy, roll back, redeploy — the sequence a real project goes
	// through, in one reused directory.
	for _, step := range []struct{ ref, want string }{
		{"main", "two"},
		{shas[0], "one"},
		{"main", "two"},
		{shas[0], "one"},
	} {
		if err := e.fetch(context.Background(), dc, work, "file://"+repo, step.ref); err != nil {
			t.Fatalf("fetch %s: %v", step.ref, err)
		}
		got, err := os.ReadFile(filepath.Join(work, "file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != step.want {
			t.Fatalf("after fetching %s the checkout holds %q, want %q", step.ref, got, step.want)
		}
	}
}

// Untracked build output must not survive into the next deployment, or a
// stale artifact can be mistaken for a fresh one.
func TestFetchCleansStaleBuildOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo, _ := makeRepo(t, []map[string]string{{"file.txt": "one"}})

	home := t.TempDir()
	t.Setenv("PUBLIX_HOME", home)
	st, _ := store.OpenAt(filepath.Join(home, "publix.json"))
	e := &Engine{store: st}

	work := filepath.Join(t.TempDir(), "checkout")
	dc := &Context{Log: discardLog(t)}
	if err := e.fetch(context.Background(), dc, work, "file://"+repo, "main"); err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(filepath.Join(work, "dist"), 0o755)
	os.WriteFile(filepath.Join(work, "dist", "stale.js"), []byte("old"), 0o644)
	os.MkdirAll(filepath.Join(work, "node_modules"), 0o755)
	os.WriteFile(filepath.Join(work, "node_modules", "dep.js"), []byte("dep"), 0o644)

	if err := e.fetch(context.Background(), dc, work, "file://"+repo, "main"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(work, "dist", "stale.js")); !os.IsNotExist(err) {
		t.Error("stale build output survived the next checkout")
	}
	// Dependencies are kept: re-downloading them every deploy is the
	// slowest thing a build can do.
	if _, err := os.Stat(filepath.Join(work, "node_modules", "dep.js")); err != nil {
		t.Error("node_modules should be preserved between deploys")
	}
}

// A clone URL carrying a token must never reach a log the dashboard shows.
func TestRedactURLCredentials(t *testing.T) {
	cases := map[string]string{
		"https://x-access-token:ghp_secret123@github.com/acme/app.git": "https://***@github.com/acme/app.git",
		"fatal: could not read from https://user:pw@example.com/r.git": "fatal: could not read from https://***@example.com/r.git",
		"https://github.com/acme/app.git":                              "https://github.com/acme/app.git",
		"no url here at all":                                           "no url here at all",
	}
	for in, want := range cases {
		if got := redactURLCredentials(in); got != want {
			t.Errorf("redactURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(redactURLCredentials("https://u:ghp_tok@github.com/a/b"), "ghp_tok") {
		t.Error("a token survived redaction")
	}
}
