package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/compose"
	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
	"gopkg.in/yaml.v3"
)

// startCompose brings up a Compose stack.
//
// publix does not rewrite the user's compose file. It writes a second file
// containing only its own additions — labels, environment, shared volumes —
// and hands both to Compose, which merges them. The repository's file stays
// the authority on what the stack is; publix only says how it is wired into
// the platform.
func (e *Engine) startCompose(ctx context.Context, dc *Context) ([]string, error) {
	sp := dc.Spec
	base := filepath.Join(dc.Root, sp.Compose)
	project := traefik.ComposeProject(dc.Project.Slug)
	dc.Service = traefik.ServiceName(dc.Project.Slug, traefik.ComposeDeploymentKey)

	f, err := compose.Parse(base)
	if err != nil {
		return nil, err
	}

	overridePath, err := e.writeComposeOverride(dc, f)
	if err != nil {
		return nil, err
	}

	dc.Log.Printf("Bringing up the compose stack %q (%d service(s))", project, len(f.Services))
	args := []string{
		"compose",
		"--project-name", project,
		"--project-directory", dc.Root,
		"-f", base,
		"-f", overridePath,
		"up", "--detach", "--remove-orphans",
	}
	if f.HasBuild() {
		args = append(args, "--build")
	}
	if err := e.composeCmd(ctx, dc, 45*time.Minute, args...); err != nil {
		return nil, err
	}

	// The override already put the routed services on the shared network, so
	// this attaches nothing in the normal case. It stays because it is also
	// what collects the container ids the health gate waits on, and because
	// a stack brought up by an older publix is still fixed by it.
	routed, err := e.attachComposeNetwork(ctx, dc, project)
	if err != nil {
		return nil, err
	}
	return routed, nil
}

// writeComposeOverride generates publix's additions to the stack. It lives
// outside the checkout so a build never dirties the repository.
func (e *Engine) writeComposeOverride(dc *Context, f *compose.File) (string, error) {
	env, err := e.buildEnv(dc)
	if err != nil {
		return "", err
	}
	binds, err := e.resolveVolumes(dc)
	if err != nil {
		return "", err
	}
	for _, b := range binds {
		dc.Log.Printf("Mounting shared volume %q at %s", b.Volume, b.MountPath)
	}
	// Compose interpolates ${VAR} in the compose file itself, from its own
	// process environment — not from the environment given to a service. A
	// compose file using it would otherwise resolve to empty strings and
	// start, which is worse than failing, so the project's environment has
	// to reach the compose process too.
	dc.ComposeEnv = env

	meta := traefik.Meta{
		ProjectID:  dc.Project.ID,
		Slug:       dc.Project.Slug,
		Deployment: dc.Deployment.ID,
		Port:       dc.Spec.Port,
		Commit:     dc.Deployment.Commit,
		Branch:     dc.Deployment.Branch,
		Kind:       string(dc.Spec.Kind),
		Created:    time.Now().UTC().Format(time.RFC3339),
	}

	routed := routedServices(dc.Spec)

	tcpByService := map[string][]deployspec.TCPRoute{}
	for _, t := range dc.Spec.TCP {
		if t.Service == "" || t.Port <= 0 {
			continue
		}
		tcpByService[t.Service] = append(tcpByService[t.Service], t)
	}
	portsByService := map[string][]deployspec.Port{}
	for _, port := range dc.Spec.Ports {
		if port.Service == "" {
			continue
		}
		portsByService[port.Service] = append(portsByService[port.Service], port)
	}

	var servicePorts map[string]int
	envMapping := envMap(env)
	services := map[string]any{}

	shared := dc.Settings.Network
	sharedUsed := false

	for _, name := range f.ServiceNames() {
		svc := map[string]any{}
		labels := traefik.BaseLabels(meta, traefik.RoleCompose)
		labels["com.docker.compose.service"] = name

		if routed[name] {
			// Join the shared network here, before the container starts,
			// rather than only afterwards: Traefik reads a container's
			// networks when it first sees it and does not look again when
			// one is connected later, so a container attached after the
			// fact is advertised on the address of whichever network it
			// happened to start with — one Traefik cannot reach.
			sharedUsed = true
			svc["networks"] = sharedNetworks(f.Services[name], shared,
				composeAliases(dc.Project.Slug, name, dc.Spec.Service))

			// The Traefik service name is stable across deploys for a
			// compose stack, so the routing file does not change when the
			// stack is redeployed in place.
			key := traefik.ComposeDeploymentKey
			svcName := traefik.ServiceName(dc.Project.Slug, key)
			if name != dc.Spec.Service {
				svcName = traefik.ServiceName(dc.Project.Slug+"-"+traefik.Slug(name), key)
			}
			port := dc.Spec.Port
			if name != dc.Spec.Service {
				port = firstPortOf(f, name, port)
			}
			// Health checks have to use the same port Traefik will send
			// traffic to. Deciding it once, here, is what keeps the probe
			// from testing a port the service does not listen on.
			if servicePorts == nil {
				servicePorts = map[string]int{}
			}
			servicePorts[name] = port
			for k, v := range composeRouterLabels(&dc.Settings, svcName, port) {
				labels[k] = v
			}
			// The TCP service lives on the container; the router that points
			// at it lives in the managed file, so a cutover moves it without
			// the container being recreated — the same split the HTTP
			// routing uses.
			for _, t := range tcpByService[name] {
				tsvc := traefik.TCPServiceFor(dc.Project.Slug, key, t)
				labels["traefik.tcp.services."+tsvc+".loadbalancer.server.port"] = itoa(t.Port)
			}
		} else {
			labels["traefik.enable"] = "false"
		}

		svc["labels"] = labels
		if len(envMapping) > 0 {
			svc["environment"] = envMapping
		}
		// Compose merges `ports` by appending, so a port declared here sits
		// beside anything the repository's own compose file publishes rather
		// than replacing it.
		if published := portsByService[name]; len(published) > 0 {
			out := make([]string, 0, len(published))
			for _, port := range published {
				out = append(out, composePort(port))
			}
			svc["ports"] = out
		}
		if mounts := bindsForService(binds, name); len(mounts) > 0 {
			vols := make([]string, 0, len(mounts))
			for _, b := range mounts {
				vols = append(vols, b.String())
			}
			svc["volumes"] = vols
		}
		services[name] = svc
	}

	dc.ServicePorts = servicePorts

	doc := map[string]any{"services": services}
	if sharedUsed {
		// The network belongs to the platform, not to the stack: publix
		// created it long before this deploy and other projects sit on it.
		doc["networks"] = map[string]any{shared: map[string]any{"external": true}}
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	header := []byte("# Generated by publix. Merged over your compose file at deploy time.\n" +
		"# Your repository is never modified; this file lives outside the checkout.\n")

	dir := filepath.Join(e.store.Settings().WorkDir, ".overrides")
	if err := ensureDir(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, dc.Project.ID+".compose.yml")
	// 0600: the override carries the project's environment, secrets included.
	if err := os.WriteFile(path, append(header, raw...), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// composePort renders one published port in compose's short syntax.
func composePort(p deployspec.Port) string {
	host := itoa(p.Host)
	if p.Bind != "" {
		host = p.Bind + ":" + host
	}
	return host + ":" + itoa(p.Target()) + "/" + p.Proto()
}

// routedServices names every compose service Traefik has to reach: the
// primary one, anything an HTTP route names, and anything a TCP route names.
//
// A service reached only over TCP — git over SSH, a database port — has no
// hostname pointing at it, but it is still routed. Deciding this in one
// place is what stops the labels and the network attachment from disagreeing
// about which containers matter, which would leave a service advertised to
// Traefik on a network Traefik cannot see.
func routedServices(sp *deployspec.Resolved) map[string]bool {
	routed := map[string]bool{sp.Service: true}
	for _, r := range sp.Routes {
		if r.Service != "" {
			routed[r.Service] = true
		}
	}
	for _, t := range sp.TCP {
		if t.Service != "" {
			routed[t.Service] = true
		}
	}
	return routed
}

// sharedNetworks is a routed service's `networks` block: the shared network
// it has to answer on, plus `default` when the stack's own file names none.
//
// Compose merges a service's networks by key, so naming one here adds to
// whatever the repository's file declared. The exception is a service that
// declared nothing at all: it was on the implicit default network, and
// naming any network would take that away and cut it off from the rest of
// the stack. Naming `default` explicitly keeps it.
func sharedNetworks(svc compose.Service, network string, aliases []string) map[string]any {
	nets := map[string]any{network: map[string]any{"aliases": aliases}}
	if svc.Networks == nil {
		nets["default"] = nil
	}
	return nets
}

// composeAliases are the names a routed container answers to on the shared
// network. The primary service also answers to the project's slug, which is
// what a route with no service of its own reaches.
func composeAliases(slug, service, primary string) []string {
	aliases := []string{slug + "-" + traefik.Slug(service)}
	if service == primary {
		aliases = append(aliases, slug)
	}
	return aliases
}

// composeRouterLabels are the Traefik labels for a routed compose service.
func composeRouterLabels(set *store.Settings, svcName string, port int) map[string]string {
	l := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": set.Network,
	}
	if port > 0 {
		l["traefik.http.services."+svcName+".loadbalancer.server.port"] = itoa(port)
	}
	return l
}

// attachComposeNetwork connects the stack's routed containers to the shared
// publix network and returns their IDs for the health gate.
func (e *Engine) attachComposeNetwork(ctx context.Context, dc *Context, project string) ([]string, error) {
	containers, err := e.docker.ListContainers(ctx, true, "com.docker.compose.project="+project)
	if err != nil {
		return nil, err
	}
	if len(containers) == 0 {
		return nil, fmt.Errorf("compose reported success but started no containers for %q", project)
	}

	routed := routedServices(dc.Spec)

	var out []string
	for _, c := range containers {
		service := c.Labels["com.docker.compose.service"]
		if !routed[service] {
			continue
		}
		if port, ok := dc.ServicePorts[service]; ok && port > 0 {
			if dc.ProbePorts == nil {
				dc.ProbePorts = map[string]int{}
			}
			dc.ProbePorts[c.ID] = port
		}
		aliases := composeAliases(dc.Project.Slug, service, dc.Spec.Service)
		if err := e.docker.ConnectNetwork(ctx, dc.Settings.Network, c.ID, aliases); err != nil {
			return nil, fmt.Errorf("attaching %s to the %q network: %w", c.Name(), dc.Settings.Network, err)
		}
		out = append(out, c.ID)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("compose service %q started no containers", dc.Spec.Service)
	}
	sort.Strings(out)
	return out, nil
}

// rollbackCompose is the failure path for a compose deploy.
//
// Unlike a Dockerfile deploy, there is no untouched previous generation to
// fall back to: Compose replaced the containers in place. The honest thing
// is to say so rather than pretend a rollback happened, and to leave the
// stack up so its logs can be read.
func (e *Engine) rollbackCompose(ctx context.Context, dc *Context) {
	dc.Log.Printf("This compose stack was replaced in place, so there is no previous generation to restore.")
	dc.Log.Printf("The stack has been left running so you can inspect it. Roll back to a previous deployment to rebuild it from that commit.")
}

// ComposeDown removes a compose stack entirely. Named volumes are kept
// unless volumes is set, because destroying a stack's data is a decision
// that has to be made explicitly.
func (e *Engine) ComposeDown(ctx context.Context, slug string, volumes bool) error {
	args := []string{"compose", "--project-name", traefik.ComposeProject(slug), "down", "--remove-orphans"}
	if volumes {
		args = append(args, "--volumes")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = append(os.Environ(), "COMPOSE_PROGRESS=plain")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose down: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// composeCmd runs a docker compose command, relaying its output live.
func (e *Engine) composeCmd(ctx context.Context, dc *Context, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := dc.Log.Writer(buildlog.StreamStdout)
	stderr := dc.Log.Writer(buildlog.StreamStderr)
	defer stdout.Flush()
	defer stderr.Flush()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dc.Root
	cmd.Env = append(os.Environ(),
		"COMPOSE_PROGRESS=plain", // interactive progress renders as noise in a log
		"DOCKER_BUILDKIT=1",
		"COMPOSE_DOCKER_CLI_BUILD=1",
	)
	// Last wins, so the project's own values override anything the host
	// happens to have set under the same name.
	cmd.Env = append(cmd.Env, dc.ComposeEnv...)
	// Keep the tail of the output as well as streaming it, so a failure can
	// be explained rather than reported as an exit status the reader then
	// has to go and interpret for themselves.
	tail := &tailBuffer{limit: 8 << 10}
	cmd.Stdout = io.MultiWriter(stdout, tail)
	cmd.Stderr = io.MultiWriter(stderr, tail)

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("docker compose %s timed out after %s", args[1], timeout)
		}
		if hint := composeFailure(tail.String()); hint != "" {
			return fmt.Errorf("%s", hint)
		}
		return fmt.Errorf("docker compose %s failed: %w", argAfterFlags(args), err)
	}
	return nil
}

// tailBuffer keeps the last limit bytes written to it and discards the rest.
// A compose run can produce megabytes; only the end explains a failure.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if excess := len(t.buf) - t.limit; excess > 0 {
		t.buf = t.buf[excess:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// imageRefRe pulls the image out of the messages Docker produces when it
// cannot get one.
var imageRefRe = regexp.MustCompile(`(?:failed to resolve reference|pull access denied for|manifest for)\s+"?([^\s",:]+(?::[^\s",]+)?)`)

// composeFailure turns compose's output into a sentence about what went
// wrong, or "" when there is nothing better to say than the exit status.
//
// The case worth naming is an image that is not in the registry yet. A
// project whose images are built by CI is deployed by pushing, and anyone
// who deploys before that build has published sees a pull failure buried in
// output about several containers. What they need to be told is that the
// image does not exist yet — not that compose exited 18.
func composeFailure(out string) string {
	lower := strings.ToLower(out)

	image := ""
	if m := imageRefRe.FindStringSubmatch(out); len(m) > 1 {
		image = m[1]
	}
	named := func(msg string) string {
		if image != "" {
			return msg + ":\n  " + image
		}
		return msg
	}

	switch {
	case strings.Contains(lower, "manifest unknown"),
		strings.Contains(lower, "failed to resolve reference") && strings.Contains(lower, "not found"):
		return named("the image this deployment needs is not in the registry") +
			"\n\nIf the images are built by CI, that build has probably not published yet — wait for it" +
			" rather than deploying again. Nothing was changed: what was already running is still serving."

	case strings.Contains(lower, "pull access denied"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "requested access to the resource is denied"):
		return named("the registry refused to serve the image") +
			"\n\nThe image may be private and this server not logged in to the registry," +
			" or the credentials it has may have expired."
	}
	return ""
}

// argAfterFlags names the compose subcommand for an error message.
func argAfterFlags(args []string) string {
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--project-name", "--project-directory", "-f":
			i++
		default:
			return args[i]
		}
	}
	return "compose"
}

func firstPortOf(f *compose.File, service string, fallback int) int {
	if svc, ok := f.Services[service]; ok {
		sub := compose.File{Services: map[string]compose.Service{service: svc}}
		if _, p := sub.Guess(); p > 0 {
			return p
		}
	}
	return fallback
}

func itoa(n int) string { return strconv.Itoa(n) }
