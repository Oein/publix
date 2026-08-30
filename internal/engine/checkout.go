package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/gitmeta"
	"github.com/Oein/publix/internal/store"
	"gopkg.in/yaml.v3"
)

// checkout puts the requested commit on disk.
//
// The checkout is reused between deploys of the same project. That is worth
// the small amount of care it takes: a fresh clone of a large repository on
// every push is the difference between a fifteen-second deploy and a
// two-minute one.
func (e *Engine) checkout(ctx context.Context, dc *Context, opt Options) error {
	p := dc.Project
	dir := e.workDir(dc.Settings, p)

	if p.Repo == nil {
		// A project with no repository deploys from a directory the
		// operator manages themselves.
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("project %s has no repository and no checkout at %s", p.Name, dir)
		}
		dc.Dir = dir
		dc.Root = filepath.Join(dir, p.RootDir)
		return nil
	}

	if err := ensureDir(filepath.Dir(dir), 0o755); err != nil {
		return err
	}

	cloneURL := p.Repo.CloneURL
	if e.GitAuth != nil {
		authed, err := e.GitAuth(p.Repo)
		if err != nil {
			return err
		}
		if authed != "" {
			cloneURL = authed
		}
	}

	ref := firstNonEmpty(opt.Commit, opt.Ref, p.Repo.Branch, "HEAD")
	dc.Log.Printf("Fetching %s at %s", p.Repo.FullName(), shorten(ref))

	if err := e.fetch(ctx, dc, dir, cloneURL, ref); err != nil {
		return err
	}

	dc.Dir = dir
	dc.Root = filepath.Join(dir, p.RootDir)
	if st, err := os.Stat(dc.Root); err != nil || !st.IsDir() {
		return fmt.Errorf("rootDir %q does not exist in the repository", p.RootDir)
	}

	// Record what was actually checked out, which for a branch deploy is
	// only knowable after the fetch.
	e.updateDeploymentMeta(dc, gitmeta.Read(dc.Dir), opt)
	return nil
}

// fetch brings dir to the requested ref, cloning it first if needed.
func (e *Engine) fetch(ctx context.Context, dc *Context, dir, cloneURL, ref string) error {
	isRepo := false
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		isRepo = true
	}

	if !isRepo {
		// A stale non-repo directory would make every later git command
		// fail confusingly; start clean.
		if _, err := os.Stat(dir); err == nil {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
		if err := e.git(ctx, dc, "", "clone", "--filter=blob:none", "--no-checkout", cloneURL, dir); err != nil {
			return err
		}
	} else {
		// The remote may have been re-authenticated since the last deploy.
		_ = e.git(ctx, dc, dir, "remote", "set-url", "origin", cloneURL)
	}

	if err := e.git(ctx, dc, dir, "fetch", "--force", "--prune", "origin", ref+":refs/publix/target"); err != nil {
		// A raw commit sha cannot be fetched by refspec on every server;
		// fall back to fetching everything and resolving locally.
		if err2 := e.git(ctx, dc, dir, "fetch", "--force", "origin"); err2 != nil {
			return fmt.Errorf("fetching %s: %w", ref, err)
		}
		if err2 := e.git(ctx, dc, dir, "checkout", "--force", "--detach", ref); err2 != nil {
			return fmt.Errorf("checking out %s: %w", ref, err2)
		}
	} else if err := e.git(ctx, dc, dir, "checkout", "--force", "--detach", "refs/publix/target"); err != nil {
		return err
	}

	// Remove build output left by the previous deployment so a stale
	// artifact can never be mistaken for a fresh one.
	if err := e.git(ctx, dc, dir, "clean", "-ffdx", "-e", "node_modules", "-e", ".venv"); err != nil {
		dc.Log.Printf("warning: could not clean the checkout: %v", err)
	}
	return nil
}

// git runs a git command, relaying failures into the build log.
func (e *Engine) git(ctx context.Context, dc *Context, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never block a deploy on a credential prompt
		"GIT_ASKPASS=true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// Credentials embedded in a clone URL must never reach a log the
		// dashboard displays.
		msg = redactURLCredentials(msg)
		if msg != "" {
			return fmt.Errorf("git %s: %s", args[0], msg)
		}
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// credentialsRe matches the "user:password@" portion of a URL. Tokens reach
// git through the clone URL, and git echoes that URL back in its own error
// messages, so every relayed message is scrubbed before it can be logged.
var credentialsRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@]+(?::[^/\s@]*)?@`)

// redactURLCredentials replaces credentials in any URL in s.
func redactURLCredentials(s string) string {
	return credentialsRe.ReplaceAllString(s, "${1}***@")
}

// updateDeploymentMeta records the commit metadata discovered at checkout.
func (e *Engine) updateDeploymentMeta(dc *Context, git gitmeta.Info, opt Options) {
	e.store.UpdateDeployment(dc.Project.ID, dc.Deployment.ID, func(d *store.Deployment) {
		if git.Commit != "" {
			d.Commit = git.Commit
			d.Short = git.ShortSHA
		}
		if d.Message == "" {
			d.Message = git.Message
		}
		if d.Author == "" {
			d.Author = git.Author
		}
		if git.Branch != "" && d.Branch == "" {
			d.Branch = git.Branch
		}
		dc.Deployment = d
	})
}

// loadSpec reads deployment.yaml from the checkout and resolves it against
// what is actually in the repository.
func (e *Engine) loadSpec(dc *Context) error {
	root := dc.Root
	var sp *deployspec.Spec

	if dc.Project.SpecPath != "" {
		raw, err := os.ReadFile(filepath.Join(root, dc.Project.SpecPath))
		if err != nil {
			return fmt.Errorf("reading %s: %w", dc.Project.SpecPath, err)
		}
		if sp, err = deployspec.Parse(raw); err != nil {
			return fmt.Errorf("%s: %w", dc.Project.SpecPath, err)
		}
	} else {
		var err error
		if sp, err = deployspec.Load(root); err != nil {
			return err
		}
	}

	if path, ok := deployspec.Find(root); ok && dc.Project.SpecPath == "" {
		dc.Log.Printf("Using %s", filepath.Base(path))
	} else if dc.Project.SpecPath == "" {
		dc.Log.Printf("No deployment.yaml found; deploying with detected settings")
	}

	resolved, err := sp.Resolve(root)
	if err != nil {
		return err
	}
	dc.Spec = resolved

	dc.Log.Printf("Type: %s%s", resolved.Kind, describeDetection(resolved))

	// Persist the resolved spec on the deployment, so a rollback to this
	// commit restores its configuration and not merely its code.
	if raw, err := yaml.Marshal(resolved.Spec); err == nil {
		e.store.UpdateDeployment(dc.Project.ID, dc.Deployment.ID, func(d *store.Deployment) {
			d.Spec = string(raw)
			d.Kind = string(resolved.Kind)
			dc.Deployment = d
		})
	}
	return nil
}

func describeDetection(r *deployspec.Resolved) string {
	if r.Detection.Framework == "" {
		return ""
	}
	return "  (" + r.Detection.Framework + ")"
}

func shorten(ref string) string {
	if len(ref) == 40 {
		return ref[:8]
	}
	return ref
}
