// Package gitmeta reads the repository metadata publix attaches to a
// deployment: which commit it came from, on which branch, by whom.
package gitmeta

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Info describes the commit a deployment was built from. Every field is
// optional: deploying from a tarball or a non-repo directory is legitimate,
// it just yields a deployment with no provenance.
type Info struct {
	Commit    string
	ShortSHA  string
	Branch    string
	Message   string
	Author    string
	Dirty     bool
	RemoteURL string
}

// Read gathers git metadata for dir. It never fails: a directory that is not
// a repository simply yields a zero Info.
func Read(dir string) Info {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree"); err != nil || out != "true" {
		return Info{}
	}

	var i Info
	i.Commit, _ = git(ctx, dir, "rev-parse", "HEAD")
	i.ShortSHA, _ = git(ctx, dir, "rev-parse", "--short=8", "HEAD")
	i.Message, _ = git(ctx, dir, "log", "-1", "--pretty=%s")
	i.Author, _ = git(ctx, dir, "log", "-1", "--pretty=%an")
	i.RemoteURL, _ = git(ctx, dir, "config", "--get", "remote.origin.url")

	branch, _ := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "HEAD" {
		i.Branch = branch
	} else {
		// Detached HEAD: fall back to whatever branch contains the commit,
		// which is what CI checkouts usually look like.
		if b, err := git(ctx, dir, "for-each-ref", "--format=%(refname:short)", "--points-at=HEAD", "refs/heads"); err == nil && b != "" {
			i.Branch = strings.SplitN(b, "\n", 2)[0]
		}
	}

	if status, err := git(ctx, dir, "status", "--porcelain"); err == nil && status != "" {
		i.Dirty = true
	}
	return i
}

// Clone fetches a repository at a ref into dir, reusing an existing checkout
// when one is present.
func Clone(ctx context.Context, repo, ref, dir string) error {
	if _, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree"); err == nil {
		if _, err := git(ctx, dir, "fetch", "--depth=1", "origin", ref); err != nil {
			return err
		}
		_, err := git(ctx, dir, "checkout", "--force", "FETCH_HEAD")
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", "--branch", ref, repo, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &Error{Args: []string{"clone", repo, ref}, Output: strings.TrimSpace(string(out)), Err: err}
	}
	return nil
}

// Error is a failed git invocation, carrying git's own message.
type Error struct {
	Args   []string
	Output string
	Err    error
}

func (e *Error) Error() string {
	if e.Output != "" {
		return "git " + strings.Join(e.Args, " ") + ": " + e.Output
	}
	return "git " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", &Error{Args: args, Err: err}
	}
	return strings.TrimSpace(string(out)), nil
}
