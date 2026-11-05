package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// NFSDefaultOptions is what an NFS volume mounts with unless it says
// otherwise. Hard mounts on purpose: an application that sees a short write
// because the NAS rebooted loses data, one that waits for it does not.
const NFSDefaultOptions = "nfsvers=3,hard,timeo=600,retrans=2,noatime"

// NFSVolumePrefix starts the name of every docker volume publix creates for
// an NFS export, which is how they are told from a project's own volumes.
const NFSVolumePrefix = "publix-nfs-"

// NFSMount is the export an NFS volume is served from.
//
// With this set, publix owns the mount: the Docker daemon mounts the export
// when a container starts, and refuses to start the container when it
// cannot. A host-path volume has no such guarantee. If the host's own mount
// is missing, the path is still there, and a container binds the empty
// directory underneath it and carries on writing to the wrong disk.
type NFSMount struct {
	// Server is the NAS's address. An IP is the reliable choice: the name is
	// resolved by the daemon while the host is booting.
	Server string `json:"server"`
	// Export is the absolute path the server exports.
	Export string `json:"export"`
	// Options replaces NFSDefaultOptions when set. addr= is added by publix.
	Options string `json:"options,omitempty"`
}

var (
	nfsServerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.:-]*$`)
	nfsOptionsRe = regexp.MustCompile(`^[A-Za-z0-9_=.,:-]*$`)
)

// validate lists what is wrong with an NFS source.
func (n NFSMount) validate() []string {
	var errs []string
	switch {
	case n.Server == "":
		errs = append(errs, "an NFS server is required")
	case !nfsServerRe.MatchString(n.Server):
		errs = append(errs, fmt.Sprintf("NFS server %q is not a hostname or an address", n.Server))
	case strings.HasSuffix(strings.ToLower(n.Server), ".local"):
		// Not a style point. Multicast DNS is not up yet when Docker starts
		// its containers at boot, so every mount would fail on every reboot.
		errs = append(errs, fmt.Sprintf("NFS server %q is a multicast DNS name, which does not resolve reliably at boot; use its IP address", n.Server))
	}

	clean := path.Clean(n.Export)
	switch {
	case n.Export == "":
		errs = append(errs, "an NFS export path is required")
	case !path.IsAbs(clean):
		errs = append(errs, fmt.Sprintf("NFS export %q must be absolute", n.Export))
	case clean == "/":
		errs = append(errs, "NFS export \"/\" would hand projects the server's whole tree")
	case strings.ContainsAny(n.Export, ",\n\r\x00"):
		errs = append(errs, fmt.Sprintf("NFS export %q contains a character a mount cannot carry", n.Export))
	}

	if !nfsOptionsRe.MatchString(n.Options) {
		errs = append(errs, fmt.Sprintf("NFS options %q contain a character a mount cannot carry", n.Options))
	}
	for _, opt := range strings.Split(n.Options, ",") {
		if strings.HasPrefix(strings.TrimSpace(opt), "addr=") {
			errs = append(errs, "NFS options must not set addr=; publix takes it from the server field")
		}
	}
	return errs
}

// IsNFS reports whether publix mounts this volume itself.
func (v Volume) IsNFS() bool { return v.NFS != nil }

// NFSRel returns a mount's directory relative to the export. It follows the
// same rule as Dir: a project-scoped volume resolves to a directory named
// after the project, a shared one to the volume's root. Cleaning against a
// leading slash keeps a subPath from climbing out of either.
func (v Volume) NFSRel(projectID, subPath string) string {
	rel := ""
	if !v.Shared() {
		rel = projectID
	}
	return strings.TrimPrefix(path.Join("/", rel, path.Clean("/"+subPath)), "/")
}

// NFSDockerVolume names the docker volume for one directory of the export.
// The source is hashed into the name because docker volumes are immutable:
// changing the server gives a new volume rather than a stale mount, and the
// next deploy picks it up.
func (v Volume) NFSDockerVolume(rel string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{v.NFS.Server, path.Clean(v.NFS.Export), v.NFSOptions(), rel}, "\x00")))
	return NFSVolumePrefix + v.Name + "-" + hex.EncodeToString(sum[:6])
}

// NFSOptions returns the mount options without the address.
func (v Volume) NFSOptions() string {
	if o := strings.TrimSpace(v.NFS.Options); o != "" {
		return o
	}
	return NFSDefaultOptions
}

// NFSDriverOptions are the local driver's options for one directory.
func (v Volume) NFSDriverOptions(rel string) map[string]string {
	return map[string]string{
		"type":   "nfs",
		"o":      "addr=" + v.NFS.Server + "," + v.NFSOptions(),
		"device": ":" + path.Join(path.Clean(v.NFS.Export), rel),
	}
}

// ParseNFSURL reads the nfs://server/export form the CLI takes.
func ParseNFSURL(raw string) (NFSMount, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(raw), "nfs://")
	if !ok {
		return NFSMount{}, false
	}
	server, export, ok := strings.Cut(rest, "/")
	if !ok {
		return NFSMount{}, false
	}
	return NFSMount{Server: server, Export: "/" + export}, true
}
