package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
)

// databaseView is a database as the dashboard shows it: its record without
// the password, plus what it is actually doing and what it hands the app.
type databaseView struct {
	*store.Database
	Image  string                `json:"image,omitempty"`
	Host   string                `json:"host,omitempty"`
	Port   int                   `json:"port,omitempty"`
	Volume string                `json:"volume"`
	Status engine.DatabaseStatus `json:"status"`
	// Env is what the project's containers receive, with the password
	// masked. The variable names are the useful part for anyone wiring
	// up an app; the password is never needed to do that.
	Env map[string]string `json:"env"`
	// NextBackup is when the schedule next fires.
	NextBackup *time.Time `json:"nextBackup,omitempty"`
}

func (s *Server) databaseView(ctx context.Context, p *store.Project, d *store.Database) databaseView {
	c := *d
	c.Password = ""
	v := databaseView{
		Database: &c,
		Image:    d.Image(),
		Volume:   d.VolumeName(p.ID),
		Status:   s.engine.DatabaseState(ctx, p, d),
		Env:      d.Env(p.ID),
	}
	if d.Engine.HasServer() {
		v.Host, v.Port = d.Host(p.ID), d.Engine.Port()
	}
	for k, val := range v.Env {
		if d.Password != "" {
			v.Env[k] = strings.ReplaceAll(val, d.Password, "••••••••")
		}
	}
	if d.Backup != nil && d.Backup.Schedule != "" {
		if sched, err := engine.ParseSchedule(d.Backup.Schedule); err == nil {
			next := sched.Next(time.Now())
			v.NextBackup = &next
		}
	}
	return v
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := make([]databaseView, 0, len(p.Databases))
	for _, d := range p.Databases {
		out = append(out, s.databaseView(ctx, p, d))
	}
	writeJSON(w, http.StatusOK, out)
}

// database resolves the {db} path value, writing the error if it is absent.
func (s *Server) database(w http.ResponseWriter, r *http.Request) (*store.Project, *store.Database, bool) {
	p, ok := s.project(w, r)
	if !ok {
		return nil, nil, false
	}
	d, ok := p.Database(r.PathValue("db"))
	if !ok {
		names := make([]string, 0, len(p.Databases))
		for _, x := range p.Databases {
			names = append(names, x.Name)
		}
		hint := "it has none"
		if len(names) > 0 {
			hint = "it has " + strings.Join(names, ", ")
		}
		writeError(w, http.StatusNotFound, fmt.Errorf("%s has no database named %q — %s", p.Name, r.PathValue("db"), hint))
		return nil, nil, false
	}
	return p, d, true
}

type backupPolicyBody struct {
	Schedule string `json:"schedule"`
	Target   string `json:"target"`
	Keep     *int   `json:"keep"`
}

// policy validates a backup policy against the server's targets. An empty
// schedule means no automatic backups.
func (s *Server) policy(b *backupPolicyBody) (*store.BackupPolicy, error) {
	if b == nil || strings.TrimSpace(b.Schedule) == "" {
		return nil, nil
	}
	pol := &store.BackupPolicy{Schedule: strings.TrimSpace(b.Schedule), Target: strings.TrimSpace(b.Target), Keep: 7}
	if b.Keep != nil {
		pol.Keep = *b.Keep
	}
	if _, err := engine.ParseSchedule(pol.Schedule); err != nil {
		return nil, err
	}
	set := s.store.Settings()
	if pol.Target == "" && len(set.BackupTargets) == 1 {
		pol.Target = set.BackupTargets[0].Name
	}
	if _, ok := set.BackupTarget(pol.Target); !ok {
		if len(set.BackupTargets) == 0 {
			return nil, fmt.Errorf("no backup target is registered; add one under Settings → Backups before scheduling backups")
		}
		return nil, fmt.Errorf("no backup target named %q; registered: %s", pol.Target, strings.Join(set.BackupTargetNames(), ", "))
	}
	return pol, nil
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Name      string            `json:"name"`
		Engine    string            `json:"engine"`
		Version   string            `json:"version"`
		EnvPrefix string            `json:"envPrefix"`
		Backup    *backupPolicyBody `json:"backup"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	d, err := p.NewDatabase(body.Name, store.DBEngine(strings.ToLower(strings.TrimSpace(body.Engine))), body.Version, body.EnvPrefix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if d.Backup, err = s.policy(body.Backup); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Pulling an image and initialising a data directory takes a while,
	// and an agent or a dashboard wants to know it worked, so this waits.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	var progress bytes.Buffer
	updated, err := s.engine.AddDatabase(ctx, p.ID, d, &progress)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"database": s.databaseView(ctx, updated, d),
		// The variables reach containers when they are created, so a
		// running deployment does not have them yet.
		"note": "Redeploy the project for its containers to receive " + strings.Join(d.EnvKeys(), ", ") + ".",
	})
}

func (s *Server) handleUpdateDatabase(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	var body struct {
		EnvPrefix *string           `json:"envPrefix"`
		Backup    *backupPolicyBody `json:"backup"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var pol *store.BackupPolicy
	var err error
	if body.Backup != nil {
		if pol, err = s.policy(body.Backup); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	updated, err := s.store.UpdateProject(p.ID, func(p *store.Project) error {
		cur, ok := p.Database(d.ID)
		if !ok {
			return &store.NotFoundError{Kind: "database", ID: d.Name}
		}
		next := *cur
		if body.EnvPrefix != nil {
			next.EnvPrefix = strings.ToUpper(strings.TrimSpace(*body.EnvPrefix))
		}
		if body.Backup != nil {
			next.Backup = pol
		}
		if err := p.ValidateDatabase(&next, cur.ID); err != nil {
			return err
		}
		*cur = next
		return nil
	})
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	nd, _ := updated.Database(d.ID)
	writeJSON(w, http.StatusOK, s.databaseView(r.Context(), updated, nd))
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	deleteData, _ := strconv.ParseBool(r.URL.Query().Get("deleteData"))
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.engine.RemoveDatabase(ctx, p, d, false); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// An SQLite volume is mounted by the app itself, and docker will not
	// remove a volume in use. The database is still deleted — what the
	// person asked for — and the reply says the data had to stay.
	var dataErr error
	if deleteData {
		dataErr = s.engine.RemoveDatabase(ctx, p, d, true)
	}
	if _, err := s.store.UpdateProject(p.ID, func(p *store.Project) error {
		out := p.Databases[:0]
		for _, x := range p.Databases {
			if x.ID != d.ID {
				out = append(out, x)
			}
		}
		p.Databases = out
		return nil
	}); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	resp := map[string]any{"ok": true}
	if dataErr != nil {
		resp["retainedData"] = d.VolumeName(p.ID)
		resp["note"] = "The database was removed, but its data volume could not be: " + dataErr.Error() +
			". It is most likely still mounted by the project's containers; redeploy, then run `docker volume rm " + d.VolumeName(p.ID) + "`."
	} else if !deleteData {
		resp["retainedData"] = d.VolumeName(p.ID)
		resp["note"] = "The data volume " + d.VolumeName(p.ID) + " was kept. Remove it with `docker volume rm` once you are sure, or delete with deleteData to remove it here."
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRestartDatabase(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := s.engine.RestartDatabase(ctx, p, d); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, s.databaseView(ctx, p, d))
}

func (s *Server) handleDatabaseLogs(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	tail := r.URL.Query().Get("tail")
	if n, err := strconv.Atoi(tail); err != nil || n <= 0 {
		tail = "200"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := s.engine.DatabaseLogs(ctx, p, d, tail, &buf); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")})
}

func (s *Server) handleBackupDatabase(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	var body struct {
		Target string `json:"target"`
	}
	// The body is optional: with no target named, the scheduled one is used.
	if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	rec, err := s.engine.Backup(ctx, p.ID, d.ID, body.Target, "manual")
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	target := r.URL.Query().Get("target")
	files, err := s.engine.ListBackups(ctx, p.ID, d.ID, target)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	if files == nil {
		files = []engine.BackupFile{}
	}
	history := d.Backups
	if history == nil {
		history = []store.BackupRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "history": history})
}

func (s *Server) handleRestoreDatabase(w http.ResponseWriter, r *http.Request) {
	p, d, ok := s.database(w, r)
	if !ok {
		return
	}
	var body struct {
		Target string `json:"target"`
		File   string `json:"file"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Hour)
	defer cancel()
	rec, err := s.engine.Restore(ctx, p.ID, d.ID, body.Target, body.File)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	resp := map[string]any{"record": rec}
	if d.Engine == store.EngineSQLite {
		resp["note"] = "Redeploy the project so the app reopens the restored database file."
	}
	writeJSON(w, http.StatusOK, resp)
}

// Backup targets.

type backupTargetView struct {
	store.BackupTarget
	Location string   `json:"location"`
	UsedBy   []string `json:"usedBy"`
	// HasConfig says an rclone config is stored, without returning it.
	HasConfig bool `json:"hasConfig,omitempty"`
}

func (s *Server) backupTargetViews() []backupTargetView {
	set := s.store.Settings()
	projects := s.store.Projects()
	out := make([]backupTargetView, 0, len(set.BackupTargets))
	for _, t := range set.BackupTargets {
		v := backupTargetView{
			BackupTarget: t.Redacted(),
			Location:     t.Location(),
			UsedBy:       store.BackupTargetUsage(projects, t.Name),
			HasConfig:    t.RcloneConfig != "",
		}
		if v.UsedBy == nil {
			v.UsedBy = []string{}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) handleListBackupTargets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backupTargetViews())
}

func (s *Server) handleAddBackupTarget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Type         string `json:"type"`
		Path         string `json:"path"`
		Remote       string `json:"remote"`
		RcloneConfig string `json:"rcloneConfig"`
		Description  string `json:"description"`
		// SkipTest saves without first proving the target is writable.
		SkipTest bool `json:"skipTest"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t := store.BackupTarget{
		Name:         strings.TrimSpace(body.Name),
		Type:         store.BackupTargetType(strings.TrimSpace(body.Type)),
		Remote:       strings.TrimSpace(body.Remote),
		RcloneConfig: strings.TrimSpace(body.RcloneConfig),
		Description:  strings.TrimSpace(body.Description),
	}
	if p := strings.TrimSpace(body.Path); p != "" {
		t.Path = filepath.Clean(p)
	}
	set := s.store.Settings()
	if err := set.ValidateBackupTarget(t); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !body.SkipTest {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		if err := s.engine.TestBackupTarget(ctx, t); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("publix could not write to %s: %w", t.Location(), err))
			return
		}
	}
	if err := s.store.SetSettings(func(set *store.Settings) error {
		if err := set.ValidateBackupTarget(t); err != nil {
			return err
		}
		set.BackupTargets = append(set.BackupTargets, t)
		return nil
	}); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.backupTargetViews())
}

func (s *Server) handleTestBackupTarget(w http.ResponseWriter, r *http.Request) {
	set := s.store.Settings()
	t, ok := set.BackupTarget(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("no backup target named %q", r.PathValue("name")))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if err := s.engine.TestBackupTarget(ctx, t); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("publix could not write to %s: %w", t.Location(), err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteBackupTarget unregisters a target. The backups already on it
// are left where they are.
func (s *Server) handleDeleteBackupTarget(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if users := store.BackupTargetUsage(s.store.Projects(), name); len(users) > 0 {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%q is where %s back up to — change their schedule first", name, strings.Join(users, ", ")))
		return
	}
	var found bool
	if err := s.store.SetSettings(func(set *store.Settings) error {
		out := set.BackupTargets[:0]
		for _, t := range set.BackupTargets {
			if t.Name == name {
				found = true
				continue
			}
			out = append(out, t)
		}
		set.BackupTargets = out
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("no backup target named %q", name))
		return
	}
	writeJSON(w, http.StatusOK, s.backupTargetViews())
}
