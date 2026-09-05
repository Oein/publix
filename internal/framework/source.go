// Package framework identifies what a repository is built with, and renders
// a Dockerfile for it.
//
// The point is that most repositories should not need a Dockerfile at all.
// A Next.js app is a Next.js app whether or not someone has written twenty
// lines of container boilerplate for it, and publix can write those lines
// itself — correctly, and the same way every time.
package framework

import (
	"os"
	"path/filepath"
	"strings"
)

// Source is a repository publix can look at.
//
// Detection runs against this rather than a directory so the same code
// serves both cases: a checkout on disk, and a repository on GitHub that
// has not been cloned yet. The import screen can therefore tell you what a
// repository is before it downloads a byte of it.
type Source interface {
	// Exists reports whether a path is present at the repository root.
	Exists(path string) bool
	// Read returns a file's contents. Callers tolerate an error: a file
	// that cannot be read simply contributes nothing to detection.
	Read(path string) ([]byte, error)
}

// Dir is a Source backed by a local directory.
type Dir string

// Exists reports whether path is a file under the directory.
func (d Dir) Exists(path string) bool {
	st, err := os.Stat(filepath.Join(string(d), filepath.FromSlash(path)))
	return err == nil && !st.IsDir()
}

// Read returns the contents of a file under the directory.
func (d Dir) Read(path string) ([]byte, error) {
	return os.ReadFile(filepath.Join(string(d), filepath.FromSlash(path)))
}

// Files is a Source built from a known set of root entries plus a reader.
// The GitHub import path uses it: one API call lists the root, and files
// are fetched only when detection actually needs their contents.
type Files struct {
	// Present is the set of paths known to exist.
	Present map[string]bool
	// Fetch returns a file's contents, or an error if it cannot.
	Fetch func(path string) ([]byte, error)

	cache map[string][]byte
}

// NewFiles builds a Source from a list of existing paths.
func NewFiles(paths []string, fetch func(string) ([]byte, error)) *Files {
	present := make(map[string]bool, len(paths))
	for _, p := range paths {
		present[strings.TrimPrefix(p, "./")] = true
	}
	return &Files{Present: present, Fetch: fetch, cache: map[string][]byte{}}
}

// Exists reports whether the path was listed.
func (f *Files) Exists(path string) bool { return f.Present[path] }

// Read fetches a file, caching it so detection reading the same config
// twice costs one request rather than two.
func (f *Files) Read(path string) ([]byte, error) {
	if raw, ok := f.cache[path]; ok {
		return raw, nil
	}
	if f.Fetch == nil || !f.Present[path] {
		return nil, os.ErrNotExist
	}
	raw, err := f.Fetch(path)
	if err != nil {
		return nil, err
	}
	if f.cache == nil {
		f.cache = map[string][]byte{}
	}
	f.cache[path] = raw
	return raw, nil
}

// firstExisting returns the first candidate path present in src.
func firstExisting(src Source, candidates ...string) string {
	for _, c := range candidates {
		if src.Exists(c) {
			return c
		}
	}
	return ""
}

// readText returns a file's contents as a string, or "" if unreadable.
func readText(src Source, path string) string {
	if path == "" {
		return ""
	}
	raw, err := src.Read(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// configExtensions are the suffixes a JavaScript config file may carry.
var configExtensions = []string{".js", ".mjs", ".cjs", ".ts", ".mts", ".cts"}

// findConfig locates a config file by base name, trying every extension.
func findConfig(src Source, base string) string {
	candidates := make([]string, 0, len(configExtensions))
	for _, ext := range configExtensions {
		candidates = append(candidates, base+ext)
	}
	return firstExisting(src, candidates...)
}
