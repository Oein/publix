package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/deployspec"
)

// waitHealthy blocks until every replica of the new deployment passes its
// readiness probe, or the grace period expires.
//
// This gate is the whole safety story. Traffic is not moved until it
// passes, so a deployment that cannot start is a failed deploy with the
// previous version still serving, rather than an outage.
func (e *Engine) waitHealthy(ctx context.Context, dc *Context, containers []string) error {
	h := dc.Spec.Health
	if h.Type == deployspec.HealthNone || len(containers) == 0 {
		return nil
	}

	dc.Log.Printf("Waiting for %d container(s) to become healthy (%s, up to %s)",
		len(containers), describeProbe(h), h.Grace.D())

	deadline := time.Now().Add(h.Grace.D())
	pending := append([]string(nil), containers...)
	var lastErr error
	attempts := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		remaining := pending[:0]
		for _, id := range pending {
			err := e.probe(ctx, dc, id)
			if err != nil {
				lastErr = err
				remaining = append(remaining, id)
				continue
			}
			if info, ierr := e.docker.InspectContainer(ctx, id); ierr == nil {
				dc.Log.Printf("%s is healthy", info.Name)
			}
		}
		pending = remaining
		if len(pending) == 0 {
			return nil
		}

		// A container that has already exited is never going to pass; fail
		// immediately rather than burn the whole grace period waiting.
		for _, id := range pending {
			info, err := e.docker.InspectContainer(ctx, id)
			if err != nil {
				continue
			}
			if !info.State.Running && !info.State.Restarting {
				reason := fmt.Sprintf("exit code %d", info.State.ExitCode)
				if info.State.OOMKilled {
					reason = "killed for exceeding its memory limit"
				} else if info.State.Error != "" {
					reason = info.State.Error
				}
				return fmt.Errorf("%s stopped before it became healthy (%s)", info.Name, reason)
			}
		}

		attempts++
		if attempts%10 == 0 {
			dc.Log.Printf("Still waiting on %d container(s): %v", len(pending), lastErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("deployment did not become healthy within %s: %v\n\nThe probe was %s. Increase health.grace in deployment.yaml if the app needs longer to start.",
				h.Grace.D(), lastErr, describeProbe(h))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.Interval.D()):
		}
	}
}

func describeProbe(h deployspec.Health) string {
	switch h.Type {
	case deployspec.HealthHTTP:
		return fmt.Sprintf("GET %s expecting %d", h.Path, h.Status)
	case deployspec.HealthTCP:
		return fmt.Sprintf("a TCP connection to port %d", h.Port)
	case deployspec.HealthExec:
		return fmt.Sprintf("exec %s", strings.Join(h.Command, " "))
	default:
		return "no probe"
	}
}

// probe runs one readiness check against one container.
func (e *Engine) probe(ctx context.Context, dc *Context, containerID string) error {
	h := dc.Spec.Health

	if h.Type == deployspec.HealthExec {
		ctx, cancel := context.WithTimeout(ctx, h.Timeout.D())
		defer cancel()
		res, err := e.docker.Exec(ctx, containerID, h.Command)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			out := strings.TrimSpace(res.Stderr + res.Stdout)
			if out == "" {
				return fmt.Errorf("probe exited %d", res.ExitCode)
			}
			return fmt.Errorf("probe exited %d: %s", res.ExitCode, firstLine(out))
		}
		return nil
	}

	info, err := e.docker.InspectContainer(ctx, containerID)
	if err != nil {
		return err
	}
	if !info.State.Running {
		return fmt.Errorf("container is %s", info.State.Status)
	}
	// Docker's own HEALTHCHECK, when the image declares one, is a stronger
	// signal than anything publix can probe from outside; respect it.
	if info.State.Health != nil {
		switch info.State.Health.Status {
		case "healthy":
			return nil
		case "starting":
			return fmt.Errorf("container healthcheck is still starting")
		case "unhealthy":
			return fmt.Errorf("container healthcheck reports unhealthy")
		}
	}

	ip := info.IPOn(dc.Settings.Network)
	if ip == "" {
		return fmt.Errorf("container has no address on the %q network yet", dc.Settings.Network)
	}
	port := h.Port
	if port == 0 {
		port = dc.Spec.Port
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))

	if h.Type == deployspec.HealthTCP {
		d := net.Dialer{Timeout: h.Timeout.D()}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, h.Timeout.D())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+addr+h.Path, nil)
	if err != nil {
		return err
	}
	req.Host = firstNonEmpty(h.Headers["Host"], req.Host)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "publix-health/1")
	}

	resp, err := healthClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != h.Status {
		return fmt.Errorf("%s returned %d, expected %d", h.Path, resp.StatusCode, h.Status)
	}
	return nil
}

// healthClient probes containers directly on the docker network. Redirects
// are not followed: a health endpoint answering 302 has not told us the app
// is ready, only that something is listening.
var healthClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
	},
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
