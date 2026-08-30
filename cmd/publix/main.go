// Command publix is a self-hosted deployment platform for Docker and
// Traefik: one binary that watches your GitHub repositories, builds them
// from a single deployment.yaml, and keeps exactly one live version of each
// project running behind automatic TLS.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Oein/publix/internal/cli"
)

// version is stamped at build time with -ldflags.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, version, os.Args[1:]); err != nil {
		if err == cli.ErrUsage {
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "publix: %v\n", err)
		os.Exit(1)
	}
}
