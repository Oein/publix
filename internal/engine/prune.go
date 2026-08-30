package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// pruneImages enforces the platform's image retention.
//
// Only the live deployment's image and the one before it are kept. That is
// the floor at which an instant rollback is still possible: one step back
// needs no rebuild, and anything older is rebuilt from its commit — which
// produces the same image, just more slowly. Keeping more would trade real
// disk for a speed-up on a rollback nobody performs.
func (e *Engine) pruneImages(ctx context.Context, p *store.Project) error {
	set := e.store.Settings()
	keep := set.KeepImages
	if keep < 1 {
		keep = 1
	}

	// Protect the images backing the live and previous deployments by tag,
	// so a project that has not deployed in a while never loses its
	// rollback target to a prune triggered by a different project.
	protected := map[string]bool{}
	for _, id := range []string{p.Current, p.Previous} {
		if id == "" {
			continue
		}
		if d, ok := p.Deployment(id); ok && d.Image != "" {
			protected[d.Image] = true
		}
	}

	// Order the project's remaining images by the deployment history, so
	// "newest" means most recently deployed rather than most recently built.
	var ordered []string
	seen := map[string]bool{}
	for _, d := range p.Deployments { // already newest-first
		if d.Image != "" && !seen[d.Image] {
			seen[d.Image] = true
			ordered = append(ordered, d.Image)
		}
	}

	images, err := e.docker.ListImages(ctx, "publix.project="+p.ID)
	if err != nil {
		return err
	}

	// Any image tagged for this project but absent from the history is an
	// orphan: a build from a deployment record that has since been pruned.
	var orphans []string
	tagged := map[string]bool{}
	for _, img := range images {
		for _, t := range img.RepoTags {
			if t == "<none>:<none>" {
				continue
			}
			tagged[t] = true
			if !seen[t] {
				orphans = append(orphans, t)
			}
		}
	}
	sort.Strings(orphans)

	var doomed []string
	kept := 0
	for _, tag := range ordered {
		if !tagged[tag] {
			continue // already gone
		}
		if protected[tag] || kept < keep {
			kept++
			continue
		}
		doomed = append(doomed, tag)
	}
	doomed = append(doomed, orphans...)

	for _, tag := range doomed {
		if protected[tag] {
			continue
		}
		// A tag still referenced by a running container is refused by the
		// daemon, which is the correct outcome; RemoveImage treats that
		// conflict as a no-op rather than an error.
		_ = e.docker.RemoveImage(ctx, tag, false)
	}
	return nil
}

// PruneDeployments trims a project's deployment history and deletes the
// build logs of the records that fell off the end.
func (e *Engine) PruneDeployments(p *store.Project) {
	set := e.store.Settings()
	keep := set.KeepDeployments
	if keep < 1 || len(p.Deployments) <= keep {
		return
	}
	for _, d := range p.Deployments[keep:] {
		_ = e.logs.Remove(d.ID)
	}
}

// Teardown removes everything belonging to a project: its containers, its
// images and its checkout. Shared-volume data is deliberately left alone —
// deleting a project should not silently destroy the data it accumulated.
func (e *Engine) Teardown(ctx context.Context, p *store.Project) error {
	var problems []string

	// A compose stack has to be dismantled by compose, so that its project
	// network and any anonymous volumes go with it.
	if live := p.LiveDeployment(); live != nil && live.Kind == string(deployspec.KindCompose) {
		if err := e.ComposeDown(ctx, p.Slug, false); err != nil {
			problems = append(problems, err.Error())
		}
	}

	containers, err := e.docker.ListContainers(ctx, true, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		problems = append(problems, err.Error())
	}
	for _, c := range containers {
		if err := e.docker.RemoveContainer(ctx, c.ID, true, false); err != nil {
			problems = append(problems, "removing "+c.Name()+": "+err.Error())
		}
	}

	images, err := e.docker.ListImages(ctx, "publix.project="+p.ID)
	if err == nil {
		for _, img := range images {
			for _, t := range img.RepoTags {
				if t != "<none>:<none>" {
					_ = e.docker.RemoveImage(ctx, t, true)
				}
			}
		}
	}

	for _, d := range p.Deployments {
		_ = e.logs.Remove(d.ID)
	}

	if len(problems) > 0 {
		return &TeardownError{Problems: problems}
	}
	return nil
}

// TeardownError lists what could not be cleaned up.
type TeardownError struct{ Problems []string }

func (e *TeardownError) Error() string {
	return "project teardown was incomplete:\n  - " + strings.Join(e.Problems, "\n  - ")
}
