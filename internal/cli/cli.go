// Package cli implements publix's command line.
//
// The dashboard is the primary interface, so the CLI stays deliberately
// small: start the server, and the handful of operations worth having in a
// shell script or a recovery session when the dashboard is not reachable.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ErrUsage signals that usage was printed and the process should exit 2.
var ErrUsage = errors.New("usage")

// command is one subcommand.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string) error
}

// Run dispatches a command line.
func Run(ctx context.Context, version string, args []string) error {
	commands := []command{
		{"serve", "run the publix server and dashboard", cmdServe},
		{"projects", "list projects and their live deployment", cmdProjects},
		{"deploy", "deploy a project now", cmdDeploy},
		{"rollback", "roll a project back to an earlier deployment", cmdRollback},
		{"logs", "show a project's build or runtime logs", cmdLogs},
		{"volumes", "list or register shared volumes", cmdVolumes},
		{"validate", "check a deployment.yaml against a checkout", cmdValidate},
		{"reconcile", "rewrite Traefik's routing file from current state", cmdReconcile},
		{"version", "print the publix version", func(context.Context, []string) error {
			fmt.Println("publix " + version)
			return nil
		}},
	}

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(commands)
		if len(args) == 0 {
			return ErrUsage
		}
		return nil
	}

	setVersion(version)

	name := args[0]
	for _, c := range commands {
		if c.name == name {
			return c.run(ctx, args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "publix: unknown command %q\n\n", name)
	usage(commands)
	return ErrUsage
}

func usage(commands []command) {
	fmt.Fprint(os.Stderr, `publix — self-hosted deployments on Docker and Traefik

Usage:
  publix <command> [flags]

Commands:
`)
	tw := tabwriter.NewWriter(os.Stderr, 0, 0, 3, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	tw.Flush()
	fmt.Fprint(os.Stderr, `
Run `+"`publix <command> -h`"+` for a command's flags.

Getting started:
  publix serve                    start the server, then open the dashboard
                                  and set a password

Everything else — importing repositories, domains, environment variables,
shared volumes — is done from the dashboard.
`)
}

// flagSet builds a flag set that prints a useful message on error.
func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("publix "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// oneOf renders a list for an error message.
func oneOf(items []string) string {
	switch len(items) {
	case 0:
		return "(none)"
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
	}
}
