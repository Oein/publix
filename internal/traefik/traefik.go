// Package traefik generates publix's routing.
//
// The split between the two halves is what makes a cutover atomic:
//
//   - A deployment's containers carry Docker labels that define its Traefik
//     *service* and its own immutable URL. Traefik's docker provider
//     discovers them, load-balances across replicas, and drops them when the
//     containers go away.
//
//   - Every hostname that can move between deployments — the project's
//     production domains and its generated <slug>.<appsDomain> — lives in a
//     single YAML file publix owns and Traefik's file provider watches.
//
// Moving traffic is therefore a file rewrite that Traefik hot-reloads, with
// no container restart and no dropped connections. It is also what lets a
// deployment be health-checked on its own URL before any user reaches it.
package traefik

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
)

// Label keys publix sets on everything it creates, so it can always find
// its own resources without disturbing anything else on the host.
const (
	LabelManaged    = "publix.managed"
	LabelProject    = "publix.project"
	LabelSlug       = "publix.slug"
	LabelDeployment = "publix.deployment"
	LabelReplica    = "publix.replica"
	LabelPort       = "publix.port"
	LabelCommit     = "publix.commit"
	LabelBranch     = "publix.branch"
	LabelCreated    = "publix.created"
	LabelImage      = "publix.image"
	LabelRole       = "publix.role"
	LabelCron       = "publix.cron"
	LabelKind       = "publix.kind"
)

// Roles distinguish the kinds of container publix creates.
const (
	RoleApp     = "app"
	RoleCompose = "compose"
	RoleCron    = "cron"
)

// ServiceName is the Traefik service backing one deployment. Every replica
// declares it, so Traefik balances across them without further configuration.
func ServiceName(slug, deployment string) string { return "publix-" + slug + "-" + deployment }

// ContainerName is the docker name of one replica.
func ContainerName(slug, deployment string, replica int) string {
	return "publix-" + slug + "-" + deployment + "-" + strconv.Itoa(replica)
}

// ComposeProject is the `docker compose -p` name for a project.
//
// It deliberately does NOT include the deployment ID. Compose namespaces its
// named volumes by project name, so a per-deployment name would hand every
// redeploy a brand-new empty volume and silently destroy the stack's data.
// A stable name means Compose replaces the containers in place and the data
// survives, which is why compose projects use the recreate strategy.
func ComposeProject(slug string) string { return "publix-" + slug }

// ComposeDeploymentKey stands in for a deployment ID in a compose stack's
// Traefik service name. Because a compose stack is replaced in place rather
// than run alongside its predecessor, its service name is stable across
// deploys and the routing file does not have to change when it redeploys.
const ComposeDeploymentKey = "stack"

// DeploymentRouter is the router for a deployment's own immutable URL.
func DeploymentRouter(slug, deployment string) string { return "publix-d-" + slug + "-" + deployment }

// DeploymentHost is the address that always points at exactly one
// deployment, e.g. "myapp-a1b2c3d4.apps.example.com". publix health-checks
// and the dashboard's "open this build" link both rely on it.
func DeploymentHost(slug, deployment, appsDomain string) string {
	if appsDomain == "" {
		return ""
	}
	return slug + "-" + deployment + "." + appsDomain
}

// ProjectHost is a project's generated production address, e.g.
// "myapp.apps.example.com". It exists so a project works the moment it is
// imported, before anyone has pointed a real domain at the host.
func ProjectHost(slug, appsDomain string) string {
	if appsDomain == "" {
		return ""
	}
	return slug + "." + appsDomain
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slug converts arbitrary text into a DNS label. Long or non-ASCII input is
// hashed rather than truncated, so two different inputs cannot collide onto
// one hostname.
func Slug(s string) string {
	out := strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(s), "-"), "-")
	const max = 32
	if out != "" && len(out) <= max {
		return out
	}
	sum := sha256.Sum256([]byte(s))
	suffix := hex.EncodeToString(sum[:])[:6]
	if out == "" {
		return "x-" + suffix
	}
	return out[:max-7] + "-" + suffix
}

// Meta is what publix records on a deployment's containers, so its state
// survives even the total loss of the store.
type Meta struct {
	ProjectID  string
	Slug       string
	Deployment string
	Replica    int
	Port       int
	Image      string
	Commit     string
	Branch     string
	Kind       string
	Created    string
}

// BaseLabels are publix's own bookkeeping labels.
func BaseLabels(m Meta, role string) map[string]string {
	l := map[string]string{
		LabelManaged:    "true",
		LabelRole:       role,
		LabelProject:    m.ProjectID,
		LabelSlug:       m.Slug,
		LabelDeployment: m.Deployment,
		LabelCreated:    m.Created,
	}
	for k, v := range map[string]string{
		LabelImage:  m.Image,
		LabelCommit: m.Commit,
		LabelBranch: m.Branch,
		LabelKind:   m.Kind,
	} {
		if v != "" {
			l[k] = v
		}
	}
	if m.Port > 0 {
		l[LabelPort] = strconv.Itoa(m.Port)
	}
	if role == RoleApp {
		l[LabelReplica] = strconv.Itoa(m.Replica)
	}
	return l
}

// RouterLabels add the Traefik service definition and the deployment's own
// immutable router.
//
// Nothing here names a production domain. That omission is the whole point:
// a container never has to be recreated for traffic to move to or from it.
func RouterLabels(set *store.Settings, sp *deployspec.Spec, m Meta) map[string]string {
	l := map[string]string{}
	if m.Port <= 0 {
		l["traefik.enable"] = "false"
		return l
	}

	svc := ServiceName(m.Slug, m.Deployment)
	l["traefik.enable"] = "true"
	l["traefik.docker.network"] = set.Network
	l["traefik.http.services."+svc+".loadbalancer.server.port"] = strconv.Itoa(m.Port)

	// Give Traefik the same readiness signal publix used at deploy time, so
	// a replica that goes bad later leaves the rotation on its own.
	if sp != nil && sp.Health.Type == deployspec.HealthHTTP {
		base := "traefik.http.services." + svc + ".loadbalancer.healthcheck."
		l[base+"path"] = sp.Health.Path
		l[base+"interval"] = sp.Health.Interval.D().String()
		l[base+"timeout"] = sp.Health.Timeout.D().String()
	}

	host := DeploymentHost(m.Slug, m.Deployment, set.AppsDomain)
	if host == "" {
		return l
	}
	rp := "traefik.http.routers." + DeploymentRouter(m.Slug, m.Deployment) + "."
	l[rp+"rule"] = "Host(`" + host + "`)"
	l[rp+"entrypoints"] = strings.Join(set.EntryPoints, ",")
	l[rp+"service"] = svc
	if set.TLSEnabled() {
		l[rp+"tls"] = "true"
		l[rp+"tls.certresolver"] = set.CertResolver
	}
	return l
}

// CronLabels mark a one-shot scheduled run. Cron containers are never routed.
func CronLabels(m Meta, job string) map[string]string {
	l := BaseLabels(m, RoleCron)
	l[LabelCron] = job
	l["traefik.enable"] = "false"
	return l
}

// Selectors for finding publix's containers by label.

// ManagedSelector matches every container publix manages on the host.
func ManagedSelector() []string { return []string{LabelManaged + "=true"} }

// ProjectSelector matches every container belonging to one project.
func ProjectSelector(projectID string) []string {
	return []string{LabelManaged + "=true", LabelProject + "=" + projectID}
}

// DeploymentSelector matches the containers of exactly one deployment.
func DeploymentSelector(projectID, deployment string) []string {
	return append(ProjectSelector(projectID), LabelDeployment+"="+deployment)
}
