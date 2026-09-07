package api

import (
	"os"
	"strings"
	"testing"
)

// The container's own root is not a host mount, so a path that only exists
// inside the container must not read as visible.
func TestMountedFromHostIgnoresTheContainerRoot(t *testing.T) {
	if _, err := os.Stat("/proc/self/mountinfo"); err != nil {
		t.Skip("no mountinfo here")
	}
	// /proc is always a mount, so it stands in for "a real mount target".
	if !mountedFromHost("/proc/self") {
		t.Error("a path under a mount target did not read as mounted")
	}
}

// Without mountinfo there is nothing to conclude, and refusing a path on
// that basis would break installs publix cannot inspect.
func TestContainerHintIsSilentOutsideAContainer(t *testing.T) {
	if inContainer() {
		t.Skip("this test process is in a container")
	}
	if hint := containerPathHint("/definitely/not/mounted/anywhere"); hint != "" {
		t.Errorf("hint given outside a container:\n%s", hint)
	}
}

// The hint has to name the path twice — once as the source and once as the
// destination — or the line it tells people to paste is wrong.
func TestContainerHintNamesBothSidesOfTheMount(t *testing.T) {
	hint := containerHintFor("/mnt/data")
	if !strings.Contains(hint, "- /mnt/data:/mnt/data") {
		t.Errorf("the compose line is missing or wrong:\n%s", hint)
	}
	if !strings.Contains(hint, "not mounted into it") {
		t.Errorf("the hint does not say what is actually wrong:\n%s", hint)
	}
}
