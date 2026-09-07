package api

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Running publix in a container is the recommended way to run it, and it
// makes one thing behave in a way nobody expects: a host path publix is
// asked about is only visible if that exact path was mounted in.
//
// Without the checks here, registering /mnt/data on a container install
// fails with "does not exist on the host" — which is false, it does exist,
// publix just cannot see it — and offering to create it would be worse
// still: the directory would be made inside the container, vanish on the
// next restart, and every project asking for the volume would bind an
// empty host directory Docker created as root.

// inContainer reports whether publix is running inside a container.
func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// mountedFromHost reports whether path, or a directory containing it, was
// mounted into this container — which is what decides whether publix is
// looking at the host's filesystem or its own.
//
// The container's own root is not a mount from the host in any useful
// sense, so it does not count.
func mountedFromHost(path string) bool {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		// Without mountinfo there is nothing to conclude. Say yes, so the
		// caller falls back to the ordinary checks rather than refusing a
		// path that may be perfectly fine.
		return true
	}
	defer f.Close()

	path = filepath.Clean(path)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// mountinfo: id parent major:minor root mountpoint options...
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		target := fields[4]
		if target == "/" {
			continue
		}
		if path == target || strings.HasPrefix(path, strings.TrimSuffix(target, "/")+"/") {
			return true
		}
	}
	return false
}

// containerPathHint explains what to do about a host path this container
// cannot see. It returns an empty string when the path is visible, or when
// publix is not in a container at all.
func containerPathHint(path string) string {
	if !inContainer() || mountedFromHost(path) {
		return ""
	}
	return containerHintFor(path)
}

// containerHintFor is the message itself, split out so it can be tested
// without a container to run in.
func containerHintFor(path string) string {
	return fmt.Sprintf(
		"publix is running in a container, and %[1]s is not mounted into it.\n\n"+
			"The path may well exist on the host — publix simply cannot see it, and\n"+
			"creating it here would make a directory inside the container that vanishes\n"+
			"on the next restart.\n\n"+
			"Mount it at the same path on both sides, in deploy/docker-compose.yml under\n"+
			"the publix service:\n\n"+
			"    volumes:\n"+
			"      - %[1]s:%[1]s\n\n"+
			"Then restart publix and register it again. The paths must match exactly:\n"+
			"publix hands them to the Docker daemon, which resolves them on the host.",
		path)
}
