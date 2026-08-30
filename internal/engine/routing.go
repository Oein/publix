package engine

import (
	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

// ReconcileRouting rewrites Traefik's routing file from the platform's
// current state.
//
// It is a full rebuild rather than an incremental edit, and it is safe to
// call at any time: the generated file is a pure function of the store, so
// a deploy, a domain change and a startup reconcile all converge on the
// same content. The write is skipped when nothing changed, so calling it
// often costs nothing.
func (e *Engine) ReconcileRouting() error {
	set := e.store.Settings()
	projects := e.store.Projects()

	live := make([]traefik.Live, 0, len(projects))
	for _, p := range projects {
		l := traefik.Live{Project: p}

		if dep := p.LiveDeployment(); dep != nil {
			l.Deployment = dep.ID
			if dep.Spec != "" {
				if sp, err := deployspec.Parse([]byte(dep.Spec)); err == nil {
					l.Spec = sp
				}
			}
			// A compose stack keeps a stable Traefik service name across
			// deploys, because its containers are replaced in place rather
			// than run alongside the previous generation.
			if dep.Kind == string(deployspec.KindCompose) {
				l.Deployment = traefik.ComposeDeploymentKey
			}
		}
		live = append(live, l)
	}

	d := traefik.Build(&set, live)
	return traefik.Write(&set, d)
}

// ProjectURL returns the address a project is reachable at, preferring a
// configured custom domain over the generated one.
func ProjectURL(set *store.Settings, p *store.Project, sp *deployspec.Spec) string {
	routes := traefik.Hosts(set, p, sp)
	scheme := "http"
	if set.TLSEnabled() {
		scheme = "https"
	}
	for _, r := range routes {
		if r.RedirectTo == "" {
			return scheme + "://" + r.Domain + r.Path
		}
	}
	return ""
}
