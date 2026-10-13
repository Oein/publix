package dockerapi

import (
	"errors"
	"fmt"
	"testing"
)

func TestCacheRestoreFailed(t *testing.T) {
	daemon := errors.New(`failed to restore cached image from "sha256:d9e8" to sha256:e923: failed to create cache image: ` +
		`failed to created cached image: creating image moby-dangling@sha256:148d failed because it already exists, but failed to resolve`)
	if !CacheRestoreFailed(fmt.Errorf("docker build failed: %w", daemon)) {
		t.Fatal("the daemon's cache restore error was not recognised")
	}
	for _, err := range []error{nil, errors.New("The command '/bin/sh -c npm ci' returned a non-zero code: 1")} {
		if CacheRestoreFailed(err) {
			t.Fatalf("%v taken for a cache restore failure", err)
		}
	}
}
