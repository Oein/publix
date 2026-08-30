package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/store"
)

// A project deployed from a directory rather than a repository has no
// commits. Its rollback still has to work, because the image that was
// serving is on disk — refusing it would strand the user with no way back.
func TestRollbackWithoutCommitUsesRetainedImage(t *testing.T) {
	h := newHarness(t)
	p := h.project("nocommit", map[string]string{
		"deployment.yaml": "port: 80\nrelease:\n  drain: 0s\n",
		"Dockerfile":      "FROM nginx:alpine\nRUN echo v1 > /usr/share/nginx/html/index.html\n",
	})

	v1 := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(v1)
	if v1.Commit != "" {
		t.Fatal("this test needs a deployment with no commit")
	}

	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	os.WriteFile(filepath.Join(dir, "Dockerfile"),
		[]byte("FROM nginx:alpine\nRUN echo v2 > /usr/share/nginx/html/index.html\n"), 0o644)
	v2 := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(v2)

	ctx := context.Background()
	plan, err := h.engine.PlanRollback(ctx, p.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Possible || !plan.Instant {
		t.Fatalf("rollback should be possible and instant, got possible=%v instant=%v: %s",
			plan.Possible, plan.Instant, plan.Reason)
	}

	dep, err := h.engine.Rollback(p.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	rolled := h.await(p.ID, dep.ID, 3*time.Minute)
	h.mustSucceed(rolled)

	if rolled.Image != v1.Image {
		t.Errorf("rollback used image %q, want the retained %q", rolled.Image, v1.Image)
	}
	if body := httpGet(t, h, h.oneContainer(ctx, p.ID), 80, "/"); !strings.Contains(body, "v1") {
		t.Errorf("after rollback the project serves %q, want v1", body)
	}
}

// A rollback restores the target deployment's configuration, not just its
// code: its domains come back with it.
func TestRollbackRestoresRecordedConfiguration(t *testing.T) {
	h := newHarness(t)
	p := h.project("cfg", map[string]string{
		"deployment.yaml": "port: 80\ndomains: [one.example.test]\nrelease:\n  drain: 0s\n",
		"Dockerfile":      "FROM nginx:alpine\n",
	})
	v1 := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(v1)

	dir := filepath.Join(h.store.Settings().WorkDir, p.ID)
	os.WriteFile(filepath.Join(dir, "deployment.yaml"),
		[]byte("port: 80\ndomains: [two.example.test]\nrelease:\n  drain: 0s\n"), 0o644)
	v2 := h.deploy(p, Options{Trigger: "test"})
	h.mustSucceed(v2)

	routing := func() string {
		raw, err := os.ReadFile(filepath.Join(h.traefik, "publix.yml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if !strings.Contains(routing(), "two.example.test") {
		t.Fatalf("expected the new domain to be routed:\n%s", routing())
	}

	dep, err := h.engine.Rollback(p.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(h.await(p.ID, dep.ID, 3*time.Minute))

	got := routing()
	if !strings.Contains(got, "one.example.test") {
		t.Errorf("rolling back should restore the domain that deployment declared:\n%s", got)
	}
	if strings.Contains(got, "two.example.test") {
		t.Errorf("the newer deployment's domain should no longer be routed:\n%s", got)
	}
}

// Rolling back to something that can be neither reused nor rebuilt must say
// so plainly rather than queueing a deploy that will fail later.
func TestRollbackImpossibleIsRefusedUpFront(t *testing.T) {
	h := newHarness(t)
	p, err := h.store.CreateProject(&store.Project{Name: "empty"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.Rollback(p.ID, ""); err == nil {
		t.Fatal("rolling back a project with no history should fail")
	} else if !strings.Contains(err.Error(), "no earlier deployment") {
		t.Errorf("unhelpful error: %v", err)
	}
}
