package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
)

// Bind is one resolved volume mount.
type Bind struct {
	// Volume is the server-registered volume this came from.
	Volume string
	// Shared reports whether every project sees this same directory.
	Shared bool
	// HostPath is the directory this project mounts from that volume.
	HostPath string
	// MountPath is where it appears inside the container.
	MountPath string
	ReadOnly  bool
	// Services limits the mount to named compose services. Empty means all.
	Services []string
}

// String renders the bind in docker's "source:target:mode" syntax.
func (b Bind) String() string {
	s := b.HostPath + ":" + b.MountPath
	if b.ReadOnly {
		s += ":ro"
	}
	return s
}

// resolveVolumes turns the project's declared volume names into concrete
// host bind mounts, creating each directory on the way.
//
// One rule is enforced here and nowhere else: a project may name a volume,
// never a path. Which directory that name resolves to is the server's
// decision, and it depends on the volume's scope — a project-scoped volume
// gives this project its own directory, a shared one gives every project
// the same directory. A repository cannot influence either way.
func (e *Engine) resolveVolumes(dc *Context) ([]Bind, error) {
	if dc.Spec == nil {
		return nil, nil
	}
	set := dc.Settings
	var binds []Bind
	var missing []string

	for _, v := range dc.Spec.Volumes {
		sv, ok := set.Volume(v.Name)
		if !ok {
			missing = append(missing, v.Name)
			continue
		}

		hostDir, err := sv.EnsureDir(dc.Project.ID)
		if err != nil {
			return nil, err
		}
		if v.SubPath != "" {
			hostDir = filepath.Join(hostDir, filepath.Clean("/"+v.SubPath))
			if err := ensureDir(hostDir, 0o777); err != nil {
				return nil, err
			}
		}

		mount := v.Mount()
		if v.MountPath == "" && sv.DefaultMount != "" {
			mount = sv.DefaultMount
		}

		binds = append(binds, Bind{
			Volume:    v.Name,
			Shared:    sv.Shared(),
			HostPath:  hostDir,
			MountPath: mount,
			// A volume the operator marked read-only stays read-only no
			// matter what the repository asks for.
			ReadOnly: v.ReadOnly || sv.ReadOnly,
			Services: v.Services,
		})
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		available := set.VolumeNames()
		hint := "no volumes are registered on this server"
		if len(available) > 0 {
			hint = "registered volumes are: " + strings.Join(available, ", ")
		}
		return nil, fmt.Errorf("deployment.yaml asks for volume(s) %s, but %s\n\nRegister them under Settings → Volumes, or remove them from deployment.yaml.",
			strings.Join(missing, ", "), hint)
	}

	sort.Slice(binds, func(i, j int) bool { return binds[i].MountPath < binds[j].MountPath })
	return binds, nil
}

// bindsForService filters binds down to those a compose service should get.
func bindsForService(binds []Bind, service string) []Bind {
	out := make([]Bind, 0, len(binds))
	for _, b := range binds {
		if len(b.Services) == 0 {
			out = append(out, b)
			continue
		}
		for _, s := range b.Services {
			if s == service {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// VolumeUsage reports which projects are using a shared volume, so the
// dashboard can refuse to unregister one that is still in use.
func VolumeUsage(projects []*store.Project, volume string) []string {
	var out []string
	for _, p := range projects {
		live := p.LiveDeployment()
		if live == nil || live.Spec == "" {
			continue
		}
		sp, err := deployspec.Parse([]byte(live.Spec))
		if err != nil {
			continue
		}
		for _, v := range sp.Volumes {
			if v.Name == volume {
				out = append(out, p.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
