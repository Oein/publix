package engine

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Oein/publix/internal/traefik"
)

// varRe matches ${namespace.KEY} and ${namespace.KEY:-default}.
var varRe = regexp.MustCompile(`\$\{([a-z]+)\.([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// buildEnv assembles the environment a deployment's containers run with.
//
// Precedence, weakest first:
//  1. publix's own metadata variables
//  2. env declared in deployment.yaml, with ${...} references expanded
//  3. env configured on the project in the dashboard
//
// The dashboard wins because that is where production credentials live, and
// a repository must never be able to override them by editing a file.
func (e *Engine) buildEnv(dc *Context) ([]string, error) {
	project := dc.Project.EnvMap()

	meta := map[string]string{
		"PROJECT_ID":    dc.Project.ID,
		"PROJECT":       dc.Project.Slug,
		"PROJECT_NAME":  dc.Project.Name,
		"DEPLOYMENT_ID": dc.Deployment.ID,
		"COMMIT":        dc.Deployment.Commit,
		"COMMIT_SHORT":  dc.Deployment.Short,
		"BRANCH":        dc.Deployment.Branch,
		"URL":           dc.URL,
	}

	out := map[string]string{
		"PUBLIX":               "1",
		"PUBLIX_PROJECT_ID":    dc.Project.ID,
		"PUBLIX_PROJECT":       dc.Project.Slug,
		"PUBLIX_DEPLOYMENT_ID": dc.Deployment.ID,
		"PUBLIX_COMMIT":        dc.Deployment.Commit,
		"PUBLIX_BRANCH":        dc.Deployment.Branch,
		"PUBLIX_URL":           dc.URL,
	}
	// Most runtimes read PORT to decide what to listen on; setting it is
	// the difference between a project working on import and not.
	if dc.Spec != nil && dc.Spec.Port > 0 {
		out["PORT"] = strconv.Itoa(dc.Spec.Port)
	}

	var unresolved []string
	if dc.Spec != nil {
		keys := make([]string, 0, len(dc.Spec.Env))
		for k := range dc.Spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := dc.Spec.Env[k]
			expanded := varRe.ReplaceAllStringFunc(v, func(m string) string {
				g := varRe.FindStringSubmatch(m)
				ns, key, def := g[1], g[2], g[3]
				var val string
				var ok bool
				switch ns {
				case "secret":
					val, ok = project[key]
				case "env":
					val, ok = os.LookupEnv(key)
				case "publix":
					val, ok = meta[key]
				default:
					unresolved = append(unresolved, fmt.Sprintf("%s: unknown namespace %q in %s (use secret, env or publix)", k, ns, m))
					return ""
				}
				if !ok {
					if strings.Contains(m, ":-") {
						return def
					}
					unresolved = append(unresolved, fmt.Sprintf("%s: %s is not set", k, m))
					return ""
				}
				return val
			})
			out[k] = expanded
		}
	}

	if len(unresolved) > 0 {
		// Booting an app with a silently blank DATABASE_URL is worse than
		// refusing to deploy, so an unresolved reference is fatal unless
		// the spec gave it an explicit ${...:-default}.
		return nil, fmt.Errorf("cannot resolve %d environment value(s):\n  - %s\n\nAdd them under the project's Environment tab, or give the reference a ${...:-default}.",
			len(unresolved), strings.Join(unresolved, "\n  - "))
	}

	for k, v := range project {
		out[k] = v
	}

	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+out[k])
	}
	return env, nil
}

// envMap converts a KEY=VALUE slice back into a map, for the compose
// override which needs mapping syntax rather than a list.
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

// deploymentURL is the address that always points at exactly one
// deployment. publix health-checks against it and the dashboard links to it.
func (e *Engine) deploymentURL(dc *Context) string {
	host := traefik.DeploymentHost(dc.Project.Slug, dc.Deployment.ID, dc.Settings.AppsDomain)
	if host == "" {
		return ""
	}
	scheme := "http"
	if dc.Settings.TLSEnabled() {
		scheme = "https"
	}
	return scheme + "://" + host
}
