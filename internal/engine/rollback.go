package engine

import (
	"context"
	"fmt"

	"github.com/Oein/publix/internal/store"
)

// Rollback returns a project to an earlier deployment.
//
// It does so by deploying that deployment's commit again, rather than by
// re-pointing traffic at containers that are no longer there. Only one
// generation is ever kept running, so "the old containers" do not exist to
// go back to — but the commit does, and rebuilding it reproduces exactly
// what was live. When that commit's image is still on disk (the immediately
// previous deployment, which is what the two-image retention guarantees)
// the build step is skipped and the rollback is as fast as a restart.
func (e *Engine) Rollback(projectID, deploymentID string) (*store.Deployment, error) {
	p, ok := e.store.Project(projectID)
	if !ok {
		return nil, &store.NotFoundError{Kind: "project", ID: projectID}
	}

	target := deploymentID
	if target == "" {
		target = p.Previous
	}
	if target == "" {
		return nil, fmt.Errorf("%s has no earlier deployment to roll back to", p.Name)
	}
	if target == p.Current {
		return nil, fmt.Errorf("deployment %s is already live", target[:min(8, len(target))])
	}

	dep, ok := p.Deployment(target)
	if !ok {
		return nil, &store.NotFoundError{Kind: "deployment", ID: target}
	}
	if dep.Commit == "" {
		return nil, fmt.Errorf("deployment %s has no recorded commit, so it cannot be rebuilt", dep.ID[:min(8, len(dep.ID))])
	}

	return e.Deploy(p.ID, Options{
		Commit:       dep.Commit,
		Ref:          dep.Branch,
		Trigger:      "rollback",
		RollbackFrom: p.Current,
		Message:      dep.Message,
		Author:       dep.Author,
	})
}

// RollbackPlan describes what a rollback would do, so the dashboard can say
// whether it will be instant or require a rebuild before the user commits.
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
	target := deploymentID
	if target == "" {
		target = p.Previous
	}
	dep, ok := p.Deployment(target)
	if !ok {
		return &RollbackPlan{Reason: "that deployment is no longer in this project's history"}, nil
	}

	plan := &RollbackPlan{Target: dep, Possible: dep.Commit != ""}
	if dep.Commit == "" {
		plan.Reason = "this deployment has no recorded commit, so it cannot be rebuilt"
		return plan, nil
	}
	if dep.Image != "" {
		if have, err := e.docker.ImageExists(ctx, dep.Image); err == nil && have {
			plan.Instant = true
			plan.Reason = "the image for this commit is still on disk, so no rebuild is needed"
			return plan, nil
		}
	}
	plan.Rebuild = true
	plan.Reason = "the image has been pruned, so this commit will be rebuilt from source"
	return plan, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
