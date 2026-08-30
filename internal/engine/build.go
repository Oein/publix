package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/store"
)

// build produces the image for a deployment.
//
// Rollback is specified as "check out that commit and build it again", and
// that is exactly what happens — with one shortcut that changes nothing
// about the result: if the image for that commit is still on disk, publix
// reuses it instead of reproducing it. Since images are tagged by commit,
// a rollback one step back is therefore instant, and a rollback to anything
// older rebuilds. Both paths deploy the same bytes the commit describes.
func (e *Engine) build(ctx context.Context, dc *Context, opt Options) error {
	if dc.Spec.Kind == deployspec.KindCompose {
		// A compose stack is built by compose itself, as part of bringing
		// the stack up, because only compose knows the build graph.
		return nil
	}

	// A rollback to a deployment whose image survived pruning deploys that
	// exact image. There is nothing to rebuild: the bytes that were live
	// are still on disk.
	if opt.Image != "" && !opt.Force {
		have, err := e.docker.ImageExists(ctx, opt.Image)
		if err != nil {
			return err
		}
		if have {
			dc.Log.Printf("Reusing image %s from the target deployment", opt.Image)
			dc.Image = opt.Image
			e.recordImage(dc, opt.Image)
			return nil
		}
		dc.Log.Printf("Image %s is no longer on disk; rebuilding from source", opt.Image)
	}

	tag := e.imageTag(dc)
	dc.Image = tag

	if !opt.Force {
		if have, err := e.docker.ImageExists(ctx, tag); err == nil && have {
			dc.Log.Printf("Reusing cached image %s (built from this commit)", tag)
			e.recordImage(dc, tag)
			return nil
		}
	}

	start := time.Now()
	switch dc.Spec.Kind {
	case deployspec.KindImage:
		if err := e.pullImage(ctx, dc, tag); err != nil {
			return err
		}
	case deployspec.KindStatic:
		if err := e.buildStatic(ctx, dc, tag); err != nil {
			return err
		}
	default:
		if err := e.buildDockerfile(ctx, dc, tag); err != nil {
			return err
		}
	}

	dc.Log.Printf("Built %s in %s", tag, time.Since(start).Round(time.Second))
	e.recordImage(dc, tag)
	return nil
}

func (e *Engine) recordImage(dc *Context, tag string) {
	e.store.UpdateDeployment(dc.Project.ID, dc.Deployment.ID, func(d *store.Deployment) { d.Image = tag })
}

// imageTag names a deployment's image.
//
// It is keyed by commit rather than by deployment ID on purpose: two
// deployments of the same commit — a redeploy, or a rollback — then resolve
// to the same tag, which is what makes the reuse above possible.
func (e *Engine) imageTag(dc *Context) string {
	key := dc.Deployment.Commit
	if key == "" {
		key = dc.Deployment.ID
	}
	if len(key) > 12 {
		key = key[:12]
	}
	return "publix/" + dc.Project.Slug + ":" + key
}

// pullImage fetches a prebuilt image and pins it to a publix-owned tag, so
// the deployment keeps working even after the upstream tag moves.
func (e *Engine) pullImage(ctx context.Context, dc *Context, tag string) error {
	ref := dc.Spec.Image
	dc.Log.Printf("Pulling %s", ref)
	if err := e.docker.PullImage(ctx, ref, dc.Log.Writer(buildlog.StreamStdout)); err != nil {
		return fmt.Errorf("pulling %s: %w", ref, err)
	}
	if err := e.docker.TagImage(ctx, ref, tag); err != nil {
		return fmt.Errorf("tagging %s: %w", ref, err)
	}
	return nil
}

// buildDockerfile builds the repository's own Dockerfile.
func (e *Engine) buildDockerfile(ctx context.Context, dc *Context, tag string) error {
	sp := dc.Spec
	buildCtx := filepath.Join(dc.Root, sp.Context)
	dc.Log.Printf("Building %s from %s", tag, sp.Dockerfile)

	out := dc.Log.Writer(buildlog.StreamStdout)
	defer out.Flush()

	err := e.docker.BuildImage(ctx, dockerapi.BuildOptions{
		Context:    buildCtx,
		Dockerfile: sp.Dockerfile,
		Tags:       []string{tag},
		Args:       sp.Build.Args,
		Target:     sp.Build.Target,
		Labels:     e.imageLabels(dc),
		CacheFrom:  e.cacheFrom(ctx, dc, tag),
		Pull:       sp.Build.Pull,
	}, out)
	if err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

// buildStatic runs the project's build command and packages its output into
// a small static-serving image.
func (e *Engine) buildStatic(ctx context.Context, dc *Context, tag string) error {
	sp := dc.Spec
	buildCtx := filepath.Join(dc.Root, sp.Context)

	env, err := e.buildEnv(dc)
	if err != nil {
		return err
	}
	env = append(env, "NODE_ENV=production", "CI=1")
	for k, v := range sp.Build.Args {
		env = append(env, k+"="+v)
	}

	for _, step := range []struct{ label, command string }{
		{"Installing dependencies", sp.Build.Install},
		{"Building", sp.Build.Command},
	} {
		if step.command == "" {
			continue
		}
		dc.Log.Printf("%s: %s", step.label, step.command)
		if err := e.shell(ctx, dc, buildCtx, env, step.command); err != nil {
			return err
		}
	}

	outDir := filepath.Join(buildCtx, sp.Build.Output)
	if st, err := os.Stat(outDir); err != nil || !st.IsDir() {
		return fmt.Errorf("build.output %q does not exist after the build\n\nCheck that %q writes to that directory.", sp.Build.Output, sp.Build.Command)
	}

	dc.Log.Printf("Packaging %s into %s", sp.Build.Output, tag)
	files := map[string][]byte{"Dockerfile.publix": staticDockerfile(sp.Spec)}
	if strings.HasPrefix(sp.Build.Runtime, "nginx") {
		files["nginx.publix.conf"] = nginxConf(sp.Spec)
	}

	out := dc.Log.Writer(buildlog.StreamStdout)
	defer out.Flush()
	return e.docker.BuildImage(ctx, dockerapi.BuildOptions{
		Context:    buildCtx,
		Dockerfile: "Dockerfile.publix",
		Tags:       []string{tag},
		Labels:     e.imageLabels(dc),
		Pull:       sp.Build.Pull,
		ExtraFiles: files,
	}, out)
}

// shell runs a build command in the checkout, relaying its output live.
func (e *Engine) shell(ctx context.Context, dc *Context, dir string, env []string, command string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	stdout := dc.Log.Writer(buildlog.StreamStdout)
	stderr := dc.Log.Writer(buildlog.StreamStderr)
	defer stdout.Flush()
	defer stderr.Flush()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%q failed: %w", command, err)
	}
	return nil
}

// staticDockerfile generates the image that serves a static build. It is
// injected into the build context rather than written to the checkout, so
// publix never leaves files behind in a repository.
func staticDockerfile(sp *deployspec.Spec) []byte {
	var b strings.Builder
	b.WriteString("# Generated by publix for a static build. Never written to your repository.\n")
	fmt.Fprintf(&b, "FROM %s\n", sp.Build.Runtime)
	if strings.HasPrefix(sp.Build.Runtime, "nginx") {
		b.WriteString("COPY nginx.publix.conf /etc/nginx/conf.d/default.conf\n")
		fmt.Fprintf(&b, "COPY %s /usr/share/nginx/html\n", sp.Build.Output)
	} else {
		fmt.Fprintf(&b, "COPY %s /site\n", sp.Build.Output)
	}
	fmt.Fprintf(&b, "EXPOSE %d\n", sp.Port)
	return []byte(b.String())
}

// nginxConf configures the static runtime.
func nginxConf(sp *deployspec.Spec) []byte {
	fallback := "=404"
	if sp.Build.SPA {
		fallback = "/index.html"
	}
	return []byte(fmt.Sprintf(`# Generated by publix.
server {
    listen %d;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;

    gzip on;
    gzip_types text/plain text/css application/json application/javascript
               text/xml application/xml image/svg+xml;
    gzip_min_length 1024;

    # Fingerprinted assets are safe to cache forever. HTML is not: caching
    # it would mean a deploy is invisible until the browser cache expires.
    location ~* \.(js|css|woff2?|png|jpg|jpeg|gif|svg|ico|webp|avif|map)$ {
        expires 1y;
        add_header Cache-Control "public, immutable";
        try_files $uri %s;
    }

    location / {
        add_header Cache-Control "no-cache";
        try_files $uri $uri/ %s;
    }
}
`, sp.Port, fallback, fallback))
}

// imageLabels make an image self-describing, so an orphan found in
// `docker images` can be traced back to the project and commit it came from.
func (e *Engine) imageLabels(dc *Context) map[string]string {
	l := map[string]string{
		"publix.managed": "true",
		"publix.project": dc.Project.ID,
		"publix.slug":    dc.Project.Slug,
	}
	if dc.Deployment.Commit != "" {
		l["publix.commit"] = dc.Deployment.Commit
		l["org.opencontainers.image.revision"] = dc.Deployment.Commit
	}
	if dc.Project.Repo != nil {
		l["org.opencontainers.image.source"] = dc.Project.Repo.CloneURL
	}
	return l
}

// cacheFrom offers this project's other images as layer sources, which is
// what makes a redeploy that only changed application code fast.
func (e *Engine) cacheFrom(ctx context.Context, dc *Context, exclude string) []string {
	images, err := e.docker.ListImages(ctx, "publix.project="+dc.Project.ID)
	if err != nil {
		return nil
	}
	var out []string
	for _, img := range images {
		for _, t := range img.RepoTags {
			if t != "<none>:<none>" && t != exclude {
				out = append(out, t)
			}
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
