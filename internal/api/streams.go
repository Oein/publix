package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/traefik"
)

// sse prepares a response for server-sent events and returns a send
// function. SSE is used rather than websockets because the traffic is
// entirely one-way and it survives proxies without special configuration.
func sse(w http.ResponseWriter, r *http.Request) (send func(event string, data any) error, ok bool) {
	flusher, isFlusher := w.(http.Flusher)
	if !isFlusher {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported by this server"))
		return nil, false
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Traefik and nginx both buffer by default, which would hold a live
	// build log until the build finished.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return func(event string, data any) error {
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}, true
}

// handleBuildLogs streams a deployment's build output.
//
// A client that connects mid-build receives everything so far and then
// follows along, so opening the page late is not a reason to miss the
// beginning of the log.
func (s *Server) handleBuildLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	deploymentID := r.PathValue("did")
	if _, found := p.Deployment(deploymentID); !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("no deployment %q in this project", deploymentID))
		return
	}

	// Without ?follow, return the whole log as JSON — which is what a
	// finished deployment needs, and what a CLI wants.
	if r.URL.Query().Get("follow") != "1" {
		lines, err := s.engine.Logs().Read(deploymentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if lines == nil {
			lines = []buildlog.Line{}
		}
		writeJSON(w, http.StatusOK, lines)
		return
	}

	send, ok := sse(w, r)
	if !ok {
		return
	}

	live, isLive := s.engine.Logs().Get(deploymentID)

	// Subscribe before replaying history, so a line written between the
	// two is delivered by the subscription rather than lost in the gap.
	var (
		stream <-chan buildlog.Line
		stop   = func() {}
	)
	if isLive {
		stream, stop = live.Subscribe()
		defer stop()
	}

	history, err := s.engine.Logs().Read(deploymentID)
	if err == nil {
		lastSeq := 0
		for _, ln := range history {
			if err := send("line", ln); err != nil {
				return
			}
			lastSeq = ln.Seq
		}
		if !isLive {
			send("done", map[string]any{"seq": lastSeq})
			return
		}
		// Drop any streamed line already covered by the history replay.
		for stream != nil {
			select {
			case ln, open := <-stream:
				if !open {
					send("done", map[string]any{"seq": lastSeq})
					return
				}
				if ln.Seq <= lastSeq {
					continue
				}
				if err := send("line", ln); err != nil {
					return
				}
			default:
			}
			break
		}
	}

	ctx := r.Context()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ln, open := <-stream:
			if !open {
				send("done", map[string]any{})
				return
			}
			if err := send("line", ln); err != nil {
				return
			}
		case <-keepalive.C:
			// An idle proxy will close a silent connection; a comment
			// frame keeps it open without confusing the client.
			if err := send("ping", map[string]any{"at": time.Now()}); err != nil {
				return
			}
		}
	}
}

// handleRuntimeLogs streams the live containers' output.
func (s *Server) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r)
	if !ok {
		return
	}
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "200"
	}
	if n, err := strconv.Atoi(tail); err != nil || n < 1 || n > 5000 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("tail must be a number between 1 and 5000"))
		return
	}

	ctx := r.Context()
	containers, err := s.docker.ListContainers(ctx, false, traefik.ProjectSelector(p.ID)...)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if len(containers) == 0 {
		if extra, err := s.docker.ListContainers(ctx, false, "com.docker.compose.project="+traefik.ComposeProject(p.Slug)); err == nil {
			containers = extra
		}
	}
	if len(containers) == 0 {
		writeError(w, http.StatusNotFound, fmt.Errorf("%s has no running containers", p.Name))
		return
	}

	follow := r.URL.Query().Get("follow") == "1"
	if !follow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		for _, c := range containers {
			fmt.Fprintf(w, "==> %s\n", c.Name())
			_ = s.docker.ContainerLogs(ctx, c.ID, dockerapi.LogOptions{
				Stdout: true, Stderr: true, Tail: tail, Timestamps: true,
			}, w)
		}
		return
	}

	send, ok := sse(w, r)
	if !ok {
		return
	}

	lines := make(chan map[string]string, 256)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, c := range containers {
		go func(id, name string) {
			w := &lineWriter{name: name, out: lines, ctx: streamCtx}
			_ = s.docker.ContainerLogs(streamCtx, id, dockerapi.LogOptions{
				Stdout: true, Stderr: true, Follow: true, Tail: tail, Timestamps: true,
			}, w)
		}(c.ID, c.Name())
	}

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ln := <-lines:
			if err := send("line", ln); err != nil {
				return
			}
		case <-keepalive.C:
			if err := send("ping", map[string]any{"at": time.Now()}); err != nil {
				return
			}
		}
	}
}

// lineWriter splits container output into SSE-sized lines tagged with the
// container they came from.
type lineWriter struct {
	name string
	out  chan<- map[string]string
	ctx  context.Context
	buf  []byte
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := indexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		select {
		case w.out <- map[string]string{"container": w.name, "text": line}:
		case <-w.ctx.Done():
			return len(b), context.Canceled
		default:
			// A client too slow to keep up loses lines rather than
			// stalling the docker stream and, through it, the daemon.
		}
	}
	if len(w.buf) > 1<<20 {
		w.buf = w.buf[:0]
	}
	return len(b), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// handleEvents streams a notification whenever platform state changes, so
// the dashboard reflects a deploy without polling.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	send, ok := sse(w, r)
	if !ok {
		return
	}
	changes, unsubscribe := s.store.Subscribe()
	defer unsubscribe()

	send("hello", map[string]any{"at": time.Now()})

	ctx := r.Context()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-changes:
			if !open {
				return
			}
			if err := send("change", map[string]any{"at": time.Now()}); err != nil {
				return
			}
		case <-keepalive.C:
			if err := send("ping", map[string]any{"at": time.Now()}); err != nil {
				return
			}
		}
	}
}
