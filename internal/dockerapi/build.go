package dockerapi

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// BuildOptions describes one image build.
type BuildOptions struct {
	// Context is the directory sent to the daemon as the build context.
	Context string
	// Dockerfile is the path to the Dockerfile relative to Context.
	Dockerfile string
	// Tags are the image tags to apply.
	Tags []string
	// Args are --build-arg values.
	Args map[string]string
	// Target selects a stage of a multi-stage build.
	Target string
	// Labels are applied to the resulting image.
	Labels map[string]string
	// CacheFrom lists images whose layers may be reused.
	CacheFrom []string
	// Pull forces base images to be refreshed.
	Pull bool
	// ExtraFiles are injected into the context, keyed by in-context path.
	// This is how publix ships a generated Dockerfile for static builds
	// without ever writing into the user's working tree.
	ExtraFiles map[string][]byte
}

// BuildImage streams a build context to the daemon and relays progress to w.
func (c *Client) BuildImage(ctx context.Context, opt BuildOptions, w io.Writer) error {
	q := url.Values{}
	q.Set("dockerfile", filepath.ToSlash(opt.Dockerfile))
	for _, t := range opt.Tags {
		q.Add("t", t)
	}
	if opt.Target != "" {
		q.Set("target", opt.Target)
	}
	if opt.Pull {
		q.Set("pull", "1")
	}
	if len(opt.Args) > 0 {
		raw, _ := json.Marshal(opt.Args)
		q.Set("buildargs", string(raw))
	}
	if len(opt.Labels) > 0 {
		raw, _ := json.Marshal(opt.Labels)
		q.Set("labels", string(raw))
	}
	if len(opt.CacheFrom) > 0 {
		raw, _ := json.Marshal(opt.CacheFrom)
		q.Set("cachefrom", string(raw))
	}
	q.Set("rm", "1")
	q.Set("forcerm", "1")

	ignore, err := loadDockerignore(opt.Context)
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeTarContext(pw, opt.Context, ignore, opt.ExtraFiles))
	}()

	resp, err := c.do(ctx, http.MethodPost, "/build", q, pr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return relayBuildOutput(resp.Body, w)
}

// buildMessage is one line of the daemon's JSON build stream.
type buildMessage struct {
	Stream      string `json:"stream"`
	Status      string `json:"status"`
	ID          string `json:"id"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Aux *struct {
		ID string `json:"ID"`
	} `json:"aux"`
}

// relayBuildOutput turns the daemon's JSON stream into readable log lines and
// surfaces build failures as errors rather than silent success.
func relayBuildOutput(r io.Reader, w io.Writer) error {
	dec := json.NewDecoder(bufio.NewReader(r))
	for {
		var m buildMessage
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading build output: %w", err)
		}
		switch {
		case m.Error != "":
			msg := m.Error
			if m.ErrorDetail != nil && m.ErrorDetail.Message != "" {
				msg = m.ErrorDetail.Message
			}
			return fmt.Errorf("%s", strings.TrimSpace(msg))
		case m.Stream != "":
			if w != nil {
				io.WriteString(w, m.Stream)
			}
		case m.Status != "":
			if w != nil {
				if m.ID != "" {
					fmt.Fprintf(w, "%s: %s\n", m.ID, m.Status)
				} else {
					fmt.Fprintf(w, "%s\n", m.Status)
				}
			}
		}
	}
}

// dockerignore holds parsed .dockerignore patterns.
type dockerignore struct{ patterns []string }

func loadDockerignore(dir string) (*dockerignore, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ".dockerignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return &dockerignore{}, nil
		}
		return nil, err
	}
	di := &dockerignore{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		di.patterns = append(di.patterns, filepath.ToSlash(strings.TrimSuffix(line, "/")))
	}
	return di, nil
}

// match reports whether rel should be excluded from the build context.
// Negations (!pattern) are honoured, last match wins, as docker does.
func (d *dockerignore) match(rel string) bool {
	excluded := false
	for _, p := range d.patterns {
		neg := strings.HasPrefix(p, "!")
		pat := strings.TrimPrefix(p, "!")
		if pathMatches(pat, rel) {
			excluded = !neg
		}
	}
	return excluded
}

// pathMatches implements docker's pattern semantics: a pattern matches the
// path itself or anything beneath it, and `**` spans directory separators.
func pathMatches(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "**") {
		re := strings.ReplaceAll(pattern, "**", "\x00")
		parts := strings.Split(re, "\x00")
		idx := 0
		for i, part := range parts {
			if part == "" {
				continue
			}
			j := strings.Index(name[idx:], part)
			if j < 0 {
				return false
			}
			if i == 0 && j != 0 {
				return false
			}
			idx += j + len(part)
		}
		return true
	}
	if ok, _ := filepath.Match(pattern, name); ok {
		return true
	}
	// A directory pattern also excludes everything inside it.
	if strings.HasPrefix(name, pattern+"/") {
		return true
	}
	// Match a bare filename at any depth, e.g. "node_modules".
	if !strings.Contains(pattern, "/") {
		for _, seg := range strings.Split(name, "/") {
			if ok, _ := filepath.Match(pattern, seg); ok {
				return true
			}
		}
	}
	return false
}

// writeTarContext streams dir as an uncompressed tar, honouring .dockerignore
// and overlaying extra generated files.
func writeTarContext(w io.Writer, dir string, ignore *dockerignore, extra map[string][]byte) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("build context %s is not a directory", dir)
	}

	written := map[string]bool{}
	for name := range extra {
		written[filepath.ToSlash(name)] = true
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		// The daemon never needs the VCS history, and it is often the
		// single largest thing in a repository.
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ignore.match(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if written[rel] {
			return nil // an injected file takes precedence
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}

	for name, content := range extra {
		hdr := &tar.Header{
			Name:     filepath.ToSlash(name),
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(content); err != nil {
			return err
		}
	}
	return nil
}

// TarDirectory writes dir to w as a gzipped tar. Used for archiving a build
// output directory into a static-serving image.
func TarDirectory(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	return writeTarContext(gz, dir, &dockerignore{}, nil)
}
