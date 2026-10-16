package dockerapi

import (
	"errors"
	"fmt"
	"testing"
)

func TestSandboxGone(t *testing.T) {
	// What the daemon answers when connecting a container that is
	// restarting after a crash.
	gone := &APIError{Status: 404, Path: "/networks/publix/connect",
		Message: "network sandbox for container 9805cd827b52 not found"}
	if !SandboxGone(fmt.Errorf("connecting: %w", gone)) {
		t.Fatal("the daemon's missing-sandbox answer was not recognised")
	}
	for _, err := range []error{
		nil,
		errors.New("network sandbox for container 9805cd827b52 not found"),
		&APIError{Status: 404, Path: "/networks/publix/connect", Message: "network publix not found"},
		&APIError{Status: 403, Path: "/networks/publix/connect", Message: "endpoint with name web already exists in network publix"},
	} {
		if SandboxGone(err) {
			t.Fatalf("%v taken for a missing sandbox", err)
		}
	}
}
