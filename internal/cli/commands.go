package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Oein/publix/internal/deployspec"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
	"github.com/Oein/publix/internal/traefik"
)

func cmdProjects(ctx context.Context, args []string) error {
	fs := flagSet("projects")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	set := st.Settings()
	projects := st.Projects()
	if len(projects) == 0 {
		fmt.Println("No projects yet. Import one from the dashboard.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tID\tSTATUS\tCOMMIT\tDEPLOYED\tURL")
	for _, p := range projects {
		status, commit, when := "—", "—", "—"
		if live := p.LiveDeployment(); live != nil {
			status = string(live.Status)
			if live.Short != "" {
				commit = live.Short
			}
			if live.FinishedAt != nil {
				when = age(*live.FinishedAt)
			}
		} else if p.Paused {
			status = "paused"
		} else {
			status = "not deployed"
		}
		url := engine.ProjectURL(&set, p, specOf(p))
		if url == "" {
			url = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Name, p.ID, status, commit, when, url)
	}
	return tw.Flush()
}

func specOf(p *store.Project) *deployspec.Spec {
	live := p.LiveDeployment()
	if live == nil || live.Spec == "" {
		return nil
	}
	sp, err := deployspec.Parse([]byte(live.Spec))
	if err != nil {
		return nil
	}
	return sp
}

func cmdDeploy(ctx context.Context, args []string) error {
	fs := flagSet("deploy")
	ref := fs.String("ref", "", "git ref to deploy (defaults to the project's branch)")
	force := fs.Bool("force", false, "rebuild even if an image for this commit already exists")
	follow := fs.Bool("f", true, "stream the build log")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: publix deploy <project> [-ref <branch>] [-force]")
	}

	p, err := open(ctx, false)
	if err != nil {
		return err
	}
	project, ok := p.store.Project(fs.Arg(0))
	if !ok {
		return unknownProject(p.store, fs.Arg(0))
	}

	dep, err := p.engine.Deploy(project.ID, engine.Options{Ref: *ref, Force: *force, Trigger: "cli"})
	if err != nil {
		return err
	}
	fmt.Printf("Deploying %s (%s)\n\n", project.Name, dep.ID)
	if !*follow {
		return nil
	}
	return followBuild(ctx, p, project.ID, dep.ID)
}

func cmdRollback(ctx context.Context, args []string) error {
	fs := flagSet("rollback")
	to := fs.String("to", "", "deployment ID to roll back to (defaults to the previous one)")
	follow := fs.Bool("f", true, "stream the build log")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: publix rollback <project> [-to <deployment>]")
	}

	p, err := open(ctx, false)
	if err != nil {
		return err
	}
	project, ok := p.store.Project(fs.Arg(0))
	if !ok {
		return unknownProject(p.store, fs.Arg(0))
	}

	plan, err := p.engine.PlanRollback(ctx, project.ID, *to)
	if err != nil {
		return err
	}
	if !plan.Possible {
		return fmt.Errorf("%s", plan.Reason)
	}
	fmt.Printf("Rolling back to %s (%s) — %s\n\n", plan.Target.Short, plan.Target.ID, plan.Reason)

	dep, err := p.engine.Rollback(project.ID, *to)
	if err != nil {
		return err
	}
	if !*follow {
		return nil
	}
	return followBuild(ctx, p, project.ID, dep.ID)
}

// followBuild prints a deployment's log until it finishes, and exits
// non-zero if the deployment failed — so this is usable in a script.
func followBuild(ctx context.Context, p *platform, projectID, deploymentID string) error {
	seen := 0
	for {
		if log, ok := p.logs.Get(deploymentID); ok {
			for _, line := range log.Snapshot() {
				if line.Seq > seen {
					fmt.Println(line.Text)
					seen = line.Seq
				}
			}
		}

		project, ok := p.store.Project(projectID)
		if !ok {
			return fmt.Errorf("the project disappeared while deploying")
		}
		dep, ok := project.Deployment(deploymentID)
		if !ok {
			return fmt.Errorf("the deployment record disappeared")
		}
		if dep.Status.Terminal() {
			// Drain anything written between the last poll and the finish.
			if lines, err := p.logs.Read(deploymentID); err == nil {
				for _, line := range lines {
					if line.Seq > seen {
						fmt.Println(line.Text)
						seen = line.Seq
					}
				}
			}
			switch dep.Status {
			case store.StatusLive:
				return nil
			default:
				return fmt.Errorf("deployment %s: %s", dep.Status, dep.Error)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func cmdLogs(ctx context.Context, args []string) error {
	fs := flagSet("logs")
	build := fs.String("build", "", "show a deployment's build log instead of runtime logs")
	tail := fs.String("tail", "200", "number of runtime log lines to show")
	follow := fs.Bool("f", false, "keep streaming")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: publix logs <project> [-build <deployment>] [-f]")
	}

	p, err := open(ctx, false)
	if err != nil {
		return err
	}
	project, ok := p.store.Project(fs.Arg(0))
	if !ok {
		return unknownProject(p.store, fs.Arg(0))
	}

	if *build != "" {
		lines, err := p.logs.Read(*build)
		if err != nil {
			return err
		}
		for _, l := range lines {
			fmt.Println(l.Text)
		}
		return nil
	}

	containers, err := p.docker.ListContainers(ctx, false, traefik.ProjectSelector(project.ID)...)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		containers, _ = p.docker.ListContainers(ctx, false, "com.docker.compose.project="+traefik.ComposeProject(project.Slug))
	}
	if len(containers) == 0 {
		return fmt.Errorf("%s has no running containers", project.Name)
	}

	for _, c := range containers {
		if len(containers) > 1 {
			fmt.Printf("==> %s\n", c.Name())
		}
		if err := p.docker.ContainerLogs(ctx, c.ID, dockerapi.LogOptions{
			Stdout: true, Stderr: true, Tail: *tail, Follow: *follow,
		}, os.Stdout); err != nil {
			return err
		}
	}
	return nil
}

func cmdVolumes(ctx context.Context, args []string) error {
	fs := flagSet("volumes")
	add := fs.String("add", "", "register a shared volume as name=/host/path")
	remove := fs.String("rm", "", "unregister a shared volume by name")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	st, err := store.Open()
	if err != nil {
		return err
	}

	switch {
	case *add != "":
		name, path, ok := strings.Cut(*add, "=")
		if !ok {
			return fmt.Errorf("usage: publix volumes -add name=/host/path")
		}
		v := store.SharedVolume{Name: strings.TrimSpace(name), Path: filepath.Clean(strings.TrimSpace(path))}
		set := st.Settings()
		if err := set.ValidateVolume(v, ""); err != nil {
			return err
		}
		if err := os.MkdirAll(v.Path, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", v.Path, err)
		}
		if err := st.SetSettings(func(set *store.Settings) error {
			if err := set.ValidateVolume(v, ""); err != nil {
				return err
			}
			set.SharedVolumes = append(set.SharedVolumes, v)
			return nil
		}); err != nil {
			return err
		}
		fmt.Printf("Registered %q at %s. Projects mount it at %s.\n", v.Name, v.Path, v.Mount())
		return nil

	case *remove != "":
		if users := engine.VolumeUsage(st.Projects(), *remove); len(users) > 0 {
			return fmt.Errorf("%q is still mounted by %s", *remove, strings.Join(users, ", "))
		}
		found := false
		if err := st.SetSettings(func(set *store.Settings) error {
			out := set.SharedVolumes[:0]
			for _, v := range set.SharedVolumes {
				if v.Name == *remove {
					found = true
					continue
				}
				out = append(out, v)
			}
			set.SharedVolumes = out
			return nil
		}); err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("no shared volume named %q", *remove)
		}
		fmt.Printf("Unregistered %q. Its data on disk was left untouched.\n", *remove)
		return nil
	}

	set := st.Settings()
	if len(set.SharedVolumes) == 0 {
		fmt.Println("No shared volumes registered.")
		fmt.Println("\nRegister one with:  publix volumes -add disk0=/mnt/data")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOST PATH\tMOUNTS AT\tUSED BY")
	projects := st.Projects()
	for _, v := range set.SharedVolumes {
		users := engine.VolumeUsage(projects, v.Name)
		used := "—"
		if len(users) > 0 {
			used = strings.Join(users, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Name, v.Path, v.Mount(), used)
	}
	tw.Flush()
	fmt.Printf("\nEach project sees its own directory: <host path>/<project id>\n")
	return nil
}

// cmdValidate checks a deployment.yaml the way a deploy would, without
// deploying anything. It is what you run before pushing.
func cmdValidate(ctx context.Context, args []string) error {
	fs := flagSet("validate")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	sp, err := deployspec.Load(abs)
	if err != nil {
		return err
	}
	path, found := deployspec.Find(abs)
	if found {
		fmt.Printf("Checking %s\n", path)
	} else {
		fmt.Printf("No deployment.yaml in %s — checking what publix would infer.\n", abs)
	}

	resolved, err := sp.Resolve(abs)
	if err != nil {
		return err
	}

	fmt.Printf("\n  type        %s", resolved.Kind)
	if resolved.Detection.Framework != "" {
		fmt.Printf("  (%s)", resolved.Detection.Framework)
	}
	fmt.Println()
	switch resolved.Kind {
	case deployspec.KindCompose:
		fmt.Printf("  compose     %s\n", resolved.Compose)
		fmt.Printf("  service     %s\n", resolved.Service)
	case deployspec.KindDockerfile:
		fmt.Printf("  dockerfile  %s\n", resolved.Dockerfile)
	case deployspec.KindStatic:
		fmt.Printf("  build       %s\n", resolved.Build.Command)
		fmt.Printf("  output      %s\n", resolved.Build.Output)
	case deployspec.KindImage:
		fmt.Printf("  image       %s\n", resolved.Image)
	}
	fmt.Printf("  port        %d\n", resolved.Port)
	fmt.Printf("  replicas    %d\n", resolved.ReplicaCount())
	fmt.Printf("  strategy    %s\n", resolved.Release.Strategy)
	if len(resolved.Routes) > 0 {
		var domains []string
		for _, r := range resolved.Routes {
			domains = append(domains, r.Domain+r.Path)
		}
		fmt.Printf("  domains     %s\n", strings.Join(domains, ", "))
	}
	if len(resolved.Volumes) > 0 {
		var vols []string
		for _, v := range resolved.Volumes {
			vols = append(vols, v.Name+" → "+v.Mount())
		}
		fmt.Printf("  volumes     %s\n", strings.Join(vols, ", "))
	}
	for _, note := range resolved.Detection.Notes {
		fmt.Printf("\n  note: %s\n", note)
	}
	fmt.Printf("\nLooks good.\n")
	return nil
}

// cmdReconcile rewrites Traefik's routing file, for when it has been lost
// or hand-edited and the dashboard is not reachable.
func cmdReconcile(ctx context.Context, args []string) error {
	fs := flagSet("reconcile")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	p, err := open(ctx, false)
	if err != nil {
		return err
	}
	if err := p.engine.ReconcileRouting(); err != nil {
		return err
	}
	set := p.store.Settings()
	fmt.Printf("Wrote %s\n", traefik.Path(&set))
	return nil
}

func unknownProject(st *store.Store, name string) error {
	var names []string
	for _, p := range st.Projects() {
		names = append(names, p.Slug)
	}
	if len(names) == 0 {
		return fmt.Errorf("no project named %q, and no projects exist yet", name)
	}
	return fmt.Errorf("no project named %q — known projects are %s", name, oneOf(names))
}

func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
