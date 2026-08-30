package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/Oein/publix/internal/store"
)

// Rollback returns a project to an earlier deployment.
//
// Only one generation is ever left running, so there are no old containers
// to point traffic back at. A rollback therefore re-creates the target
// deployment, by whichever of two routes is available:
//
//   - Its image is still on disk. The two-image retention exists precisely
//     so that the previous deployment always is, which makes a one-step
//     rollback a container start rather than a build.
//   - Otherwise, its commit is checked out and rebuilt, reproducing the
//     same deployment more slowly.
//
// Either way the target's recorded configuration comes back with it, so a
// rollback restores the domains and settings of that deployment too, not
// just its code.
func (e *Engine) Rollback(projectID, deploymentID string) (*store.Deployment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plan, err := e.PlanRollback(ctx, projectID, deploymentID)
	if err != nil {
		return nil, err
	}
	if !plan.Possible {
		return nil, fmt.Errorf("%s", plan.Reason)
	}

	p, _ := e.store.Project(projectID)
	target := plan.Target

	opt := Options{
		Trigger:      "rollback",
		RollbackFrom: p.Current,
		Ref:          target.Branch,
		Commit:       target.Commit,
		Message:      target.Message,
		Author:       target.Author,
		Spec:         target.Spec,
	}
	if plan.Instant {
		opt.Image = target.Image
	}
	return e.Deploy(p.ID, opt)
}

// RollbackPlan describes what a rollback would do, so the dashboard can say
// whether it will be instant or need a rebuild before the user commits.
type RollbackPlan struct {
	Target   *store.Deployment `json:"target"`
	Instant  bool              `json:"instant"`
	Rebuild  bool              `json:"rebuild"`
	Reason   string            `json:"reason"`
	Possible bool              `json:"possible"`
}

// PlanRollback reports what rolling back to a deployment would involve.
func (e *Engine) PlanRollback(ctx context.Context, projectID, deploymentID string) (*RollbackPlan, error) {
	p, ok := e.store.Project(projectID)
	if !ok {
		return nil, &store.NotFoundError{Kind: "project", ID: projectID}
	}

	id := deploymentID
	if id == "" {
		id = p.Previous
	}
	if id == "" {
		return &RollbackPlan{Reason: fmt.Sprintf("%s has no earlier deployment to roll back to", p.Name)}, nil
	}
	if id == p.Current {
		return &RollbackPlan{Reason: "that deployment is already live"}, nil
	}

	target, ok := p.Deployment(id)
	if !ok {
		return &RollbackPlan{Reason: "that deployment is no longer in this project's history"}, nil
	}

	plan := &RollbackPlan{Target: target}

	// Prefer the image that is already on disk. It is both faster and more
	// faithful: it is the exact artifact that was serving, not a rebuild
	// that could differ if a base image or dependency has since moved.
	if target.Image != "" {
		if have, err := e.docker.ImageExists(ctx, target.Image); err == nil && have {
			plan.Possible, plan.Instant = true, true
			plan.Reason = "the image for this deployment is still on disk, so nothing needs rebuilding"
			return plan, nil
		}
	}

	if target.Commit == "" {
		plan.Reason = "this deployment's image has been pruned and it has no recorded commit, so it cannot be rebuilt"
		return plan, nil
	}
	if p.Repo == nil {
		plan.Reason = "this deployment's image has been pruned and the project has no repository to rebuild it from"
		return plan, nil
	}

	plan.Possible, plan.Rebuild = true, true
	plan.Reason = "the image has been pruned, so this commit will be checked out and rebuilt"
	return plan, nil
}
