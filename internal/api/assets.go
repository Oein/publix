package api

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// handleAssets serves the built dashboard.
//
// The dashboard is a single-page app, so any path that is not a real file
// falls through to index.html and lets the client router take over. API
// paths are excluded from that fallback: a mistyped API route must return a
// 404, not a page of HTML that a fetch() will fail to parse.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, &notFound{r.URL.Path})
		return
	}
	if s.assets == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "The publix dashboard was not built into this binary.\n\nBuild it with `make ui` (or `npm --prefix web ci && npm --prefix web run build`) and rebuild, or use the API directly.\n")
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	f, err := s.assets.Open(name)
	if err != nil {
		s.serveIndex(w, r)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		s.serveIndex(w, r)
		return
	}

	setContentType(w, name)
	// Vite fingerprints every asset filename, so those are immutable; the
	// entry HTML must never be cached or a deploy would not be visible.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, time.Time{}, rs)
		return
	}
	io.Copy(w, f)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	raw, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		writeError(w, http.StatusNotFound, &notFound{r.URL.Path})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(raw)
}

func setContentType(w http.ResponseWriter, name string) {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
		return
	}
	switch path.Ext(name) {
	case ".js", ".mjs":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	}
}

type notFound struct{ path string }

func (e *notFound) Error() string { return "no such endpoint: " + e.path }
