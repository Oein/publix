package api

import (
	"embed"
	"io/fs"
)

// dashboard is the built Svelte app, compiled into the binary so a publix
// install is a single file with no static assets to deploy alongside it.
//
//go:embed all:dist
var dashboard embed.FS

// Dashboard returns the built dashboard, or nil if the binary was compiled
// without one (which the asset handler explains rather than 404-ing).
func Dashboard() fs.FS {
	sub, err := fs.Sub(dashboard, "dist")
	if err != nil {
		return nil
	}
	// A dist directory containing only the placeholder means the UI was
	// never built; say so clearly instead of serving a blank page.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
