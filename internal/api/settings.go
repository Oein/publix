package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// settingsView is the settings document as the dashboard sees it. Secrets
// are never included: the browser has no use for them, and a value that is
// never sent cannot be leaked by a screenshot or a browser extension.
type settingsView struct {
	Network           string             `json:"network"`
	TraefikDynamicDir string             `json:"traefikDynamicDir"`
	TraefikFile       string             `json:"traefikFile"`
	EntryPoints       []string           `json:"entryPoints"`
	CertResolver      string             `json:"certResolver"`
	AppsDomain        string             `json:"appsDomain"`
	PublicURL         string             `json:"publicUrl"`
	WorkDir           string             `json:"workDir"`
	KeepImages        int                `json:"keepImages"`
	KeepDeployments   int                `json:"keepDeployments"`
	BuildConcurrency  int                `json:"buildConcurrency"`
	SharedVolumes     []sharedVolumeView `json:"sharedVolumes"`
}

// sharedVolumeView annotates a registered volume with what it is being used
// for, so unregistering one is an informed decision.
type sharedVolumeView struct {
	store.SharedVolume
	Mount    string   `json:"mount"`
	UsedBy   []string `json:"usedBy"`
	Writable bool     `json:"writable"`
	Exists   bool     `json:"exists"`
	Error    string   `json:"error,omitempty"`
}

func (s *Server) settingsView() settingsView {
	set := s.store.Settings()
	v := settingsView{
		Network:           set.Network,
		TraefikDynamicDir: set.TraefikDynamicDir,
		TraefikFile:       traefik.Path(&set),
		EntryPoints:       set.EntryPoints,
		CertResolver:      set.CertResolver,
		AppsDomain:        set.AppsDomain,
		PublicURL:         set.PublicURL,
		WorkDir:           set.WorkDir,
		KeepImages:        set.KeepImages,
		KeepDeployments:   set.KeepDeployments,
		BuildConcurrency:  set.BuildConcurrency,
		SharedVolumes:     []sharedVolumeView{},
	}
	projects := s.store.Projects()
	for _, sv := range set.SharedVolumes {
		view := sharedVolumeView{SharedVolume: sv, Mount: sv.Mount(), UsedBy: engine.VolumeUsage(projects, sv.Name)}
		if view.UsedBy == nil {
			view.UsedBy = []string{}
		}
		if st, err := os.Stat(sv.Path); err == nil && st.IsDir() {
			view.Exists = true
			view.Writable = isWritableDir(sv.Path)
			if !view.Writable {
				view.Error = "publix cannot write to this directory"
			}
		} else {
			view.Error = "this directory does not exist on the host"
		}
		v.SharedVolumes = append(v.SharedVolumes, view)
	}
	return v
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsView())
}

// handleSetSettings updates the server configuration. Fields are pointers
// so an omitted field is left alone rather than reset to its zero value.
func (s *Server) handleSetSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Network           *string   `json:"network,omitempty"`
		TraefikDynamicDir *string   `json:"traefikDynamicDir,omitempty"`
		EntryPoints       *[]string `json:"entryPoints,omitempty"`
		CertResolver      *string   `json:"certResolver,omitempty"`
		AppsDomain        *string   `json:"appsDomain,omitempty"`
		PublicURL         *string   `json:"publicUrl,omitempty"`
		WorkDir           *string   `json:"workDir,omitempty"`
		KeepImages        *int      `json:"keepImages,omitempty"`
		KeepDeployments   *int      `json:"keepDeployments,omitempty"`
		BuildConcurrency  *int      `json:"buildConcurrency,omitempty"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := s.store.SetSettings(func(set *store.Settings) error {
		if body.Network != nil {
			if strings.TrimSpace(*body.Network) == "" {
				return fmt.Errorf("network cannot be empty")
			}
			set.Network = strings.TrimSpace(*body.Network)
		}
		if body.TraefikDynamicDir != nil {
			dir := strings.TrimSpace(*body.TraefikDynamicDir)
			if !filepath.IsAbs(dir) {
				return fmt.Errorf("the Traefik dynamic directory must be an absolute path")
			}
			set.TraefikDynamicDir = dir
		}
		if body.EntryPoints != nil {
			var eps []string
			for _, e := range *body.EntryPoints {
				if e = strings.TrimSpace(e); e != "" {
					eps = append(eps, e)
				}
			}
			if len(eps) == 0 {
				return fmt.Errorf("at least one Traefik entrypoint is required")
			}
			set.EntryPoints = eps
		}
		if body.CertResolver != nil {
			set.CertResolver = strings.TrimSpace(*body.CertResolver)
		}
		if body.AppsDomain != nil {
			d := strings.ToLower(strings.TrimSpace(*body.AppsDomain))
			d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), ".")
			if d != "" && !strings.Contains(d, ".") {
				return fmt.Errorf("%q does not look like a domain", d)
			}
			set.AppsDomain = d
		}
		if body.PublicURL != nil {
			u := strings.TrimSuffix(strings.TrimSpace(*body.PublicURL), "/")
			if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return fmt.Errorf("the public URL must start with http:// or https://")
			}
			set.PublicURL = u
		}
		if body.WorkDir != nil && strings.TrimSpace(*body.WorkDir) != "" {
			set.WorkDir = strings.TrimSpace(*body.WorkDir)
		}
		if body.KeepImages != nil {
			// One image means no instant rollback at all; two is the floor
			// that keeps the previous deployment restorable without a build.
			if *body.KeepImages < 1 || *body.KeepImages > 20 {
				return fmt.Errorf("keepImages must be between 1 and 20")
			}
			set.KeepImages = *body.KeepImages
		}
		if body.KeepDeployments != nil {
			if *body.KeepDeployments < 1 || *body.KeepDeployments > 500 {
				return fmt.Errorf("keepDeployments must be between 1 and 500")
			}
			set.KeepDeployments = *body.KeepDeployments
		}
		if body.BuildConcurrency != nil {
			if *body.BuildConcurrency < 1 || *body.BuildConcurrency > 16 {
				return fmt.Errorf("buildConcurrency must be between 1 and 16")
			}
			set.BuildConcurrency = *body.BuildConcurrency
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// The generated hostnames and routing file both derive from these
	// settings, so rewrite the routing immediately rather than leaving it
	// stale until the next deploy.
	if err := s.engine.ReconcileRouting(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsView())
}

// handleAddVolume registers a shared volume.
func (s *Server) handleAddVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Path         string `json:"path"`
		Description  string `json:"description"`
		ReadOnly     bool   `json:"readOnly"`
		DefaultMount string `json:"defaultMount"`
		// Create makes the host directory if it does not exist.
		Create bool `json:"create"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	v := store.SharedVolume{
		Name:         strings.TrimSpace(body.Name),
		Path:         filepath.Clean(strings.TrimSpace(body.Path)),
		Description:  strings.TrimSpace(body.Description),
		ReadOnly:     body.ReadOnly,
		DefaultMount: strings.TrimSpace(body.DefaultMount),
	}

	set := s.store.Settings()
	if err := set.ValidateVolume(v, ""); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if st, err := os.Stat(v.Path); err != nil {
		if !body.Create {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s does not exist on the host. Create it first, or re-submit with create: true.", v.Path))
			return
		}
		if err := os.MkdirAll(v.Path, 0o755); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("could not create %s: %w", v.Path, err))
			return
		}
	} else if !st.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%s is a file, not a directory", v.Path))
		return
	}
	if !isWritableDir(v.Path) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("publix cannot write to %s — check its ownership and permissions", v.Path))
		return
	}

	if err := s.store.SetSettings(func(set *store.Settings) error {
		if err := set.ValidateVolume(v, ""); err != nil {
			return err
		}
		set.SharedVolumes = append(set.SharedVolumes, v)
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.settingsView())
}

// handleDeleteVolume unregisters a shared volume. The host directory and
// everything in it is left alone: unregistering is a configuration change,
// not a request to delete data.
func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if users := engine.VolumeUsage(s.store.Projects(), name); len(users) > 0 {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%q is still mounted by %s — remove it from their deployment.yaml and redeploy first",
			name, strings.Join(users, ", ")))
		return
	}

	var found bool
	if err := s.store.SetSettings(func(set *store.Settings) error {
		out := set.SharedVolumes[:0]
		for _, v := range set.SharedVolumes {
			if v.Name == name {
				found = true
				continue
			}
			out = append(out, v)
		}
		set.SharedVolumes = out
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("no shared volume named %q", name))
		return
	}
	writeJSON(w, http.StatusOK, s.settingsView())
}

// handleSystem reports the health of everything publix depends on, which is
// the first thing to check when something is not working.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	set := s.store.Settings()
	out := map[string]any{
		"version":   Version,
		"storePath": s.store.Path(),
		"home":      store.Home(),
	}

	if v, err := s.docker.Ping(ctx); err != nil {
		out["docker"] = map[string]any{"ok": false, "error": err.Error()}
	} else {
		out["docker"] = map[string]any{"ok": true, "version": v.Version, "api": v.APIVersion, "os": v.Os, "arch": v.Arch}
	}

	// Traefik health is really "can publix write the file Traefik reads",
	// which is the failure people actually hit.
	traefikPath := traefik.Path(&set)
	tinfo := map[string]any{"file": traefikPath}
	if st, err := os.Stat(set.TraefikDynamicDir); err != nil {
		tinfo["ok"], tinfo["error"] = false, fmt.Sprintf("%s does not exist", set.TraefikDynamicDir)
	} else if !st.IsDir() {
		tinfo["ok"], tinfo["error"] = false, fmt.Sprintf("%s is not a directory", set.TraefikDynamicDir)
	} else if !isWritableDir(set.TraefikDynamicDir) {
		tinfo["ok"], tinfo["error"] = false, fmt.Sprintf("publix cannot write to %s", set.TraefikDynamicDir)
	} else {
		tinfo["ok"] = true
		if fi, err := os.Stat(traefikPath); err == nil {
			tinfo["written"] = fi.ModTime()
			tinfo["size"] = fi.Size()
		}
	}
	out["traefik"] = tinfo

	if nets, err := s.docker.ListNetworks(ctx); err == nil {
		found := false
		for _, n := range nets {
			if n.Name == set.Network {
				found = true
			}
		}
		out["network"] = map[string]any{"name": set.Network, "exists": found}
	}

	projects := s.store.Projects()
	live := 0
	for _, p := range projects {
		if p.Current != "" {
			live++
		}
	}
	out["projects"] = map[string]any{"total": len(projects), "live": live}
	writeJSON(w, http.StatusOK, out)
}

// Version is stamped at build time.
var Version = "dev"

// isWritableDir reports whether publix can create files in dir. Checking the
// permission bits would be wrong under a container's user namespace; the
// only reliable test is to try.
func isWritableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".publix-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
