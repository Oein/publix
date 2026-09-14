package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Oein/publix/internal/mcp"
)

// This file exposes the dashboard's entire capability set to AI agents over
// the Model Context Protocol.
//
// The tools do not reimplement anything. Each one describes an HTTP call and
// is dispatched, in process, through the very same ServeMux that serves the
// dashboard — so a tool gets the same validation, the same conflict checks,
// the same error messages and the same response shape as the button that
// does the job in the UI. There is one implementation of "deploy this
// project", not two that can drift apart.

// mcpServerInstructions is what a client is told the server is for. A model
// reads this before it reads any tool description, so it is where the shape
// of the system belongs rather than the detail of any one call.
const mcpServerInstructions = `publix is a self-hosted deployment platform running on Docker and Traefik.
It deploys projects — usually GitHub repositories — building each one into an
image, health-checking it on a private URL, and only then moving production
traffic to it.

These tools cover everything the publix dashboard can do.

Orientation:
  - list_projects is almost always the right first call. Every project tool
    takes the "project" argument, which is a project's id or its slug as
    that listing reports them.
  - A deploy is asynchronous. deploy_project returns as soon as the build is
    queued; read get_build_logs for the outcome, or get_project to see
    whether a live deployment has changed.
  - Importing a repository is a two-step job: inspect_repo shows what publix
    worked out about it and the deployment.yaml it would use, and
    import_repo then creates the project, registers the webhook and deploys.

Worth knowing before changing anything:
  - set_env and set_domains REPLACE the whole list rather than adding to it.
    Read the project first and send back the full set, or you will remove
    what you did not mention.
  - Secret environment variables are never readable. Sending a secret back
    with an empty value keeps the stored value; that is how an edit to one
    variable avoids blanking the others.
  - delete_project tears down containers and routing and cannot be undone.
    Volume data on the host is deliberately left behind.`

// mcpServer builds the tool catalogue, once.
func (s *Server) mcpServer() *mcp.Server {
	s.mcpOnce.Do(func() {
		srv := mcp.NewServer("publix", Version, mcpServerInstructions)
		for _, t := range s.mcpTools() {
			srv.Register(s.register(t))
		}
		s.mcpSrv = srv
	})
	return s.mcpSrv
}

// handleMCP serves the Streamable HTTP transport.
//
// publix never needs to push to the client — every tool answers and is done
// — so a POST is always answered with a single JSON response rather than an
// event stream, which the transport explicitly allows.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("could not read the request: %w", err))
		return
	}

	// Carry the caller's credentials down to the in-process dispatch, so a
	// tool reaches the handlers as the same authenticated user that made
	// the MCP request and no privilege is invented along the way.
	reply := s.mcpServer().Handle(withMCPCreds(r.Context(), r), body)
	if reply == nil {
		// Nothing but notifications: accepted, with no body to send.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(reply)
}

// handleMCPUnsupported answers the transport's optional server-to-client
// stream. publix has nothing to push, and saying so plainly is better than
// holding a connection open forever.
func (s *Server) handleMCPUnsupported(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("this server does not offer a server-to-client event stream; POST JSON-RPC messages instead"))
}

// handleMCPDelete ends a session. The server keeps no per-session state, so
// there is nothing to discard and every session ends successfully.
func (s *Server) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Credential plumbing.

type mcpCredsKey struct{}

type mcpCreds struct {
	authorization string
	session       string
}

func withMCPCreds(ctx context.Context, r *http.Request) context.Context {
	creds := mcpCreds{authorization: r.Header.Get("Authorization")}
	if c, err := r.Cookie(sessionCookie); err == nil {
		creds.session = c.Value
	}
	return context.WithValue(ctx, mcpCredsKey{}, creds)
}

// recorder captures a handler's response in memory. It is the whole of what
// in-process dispatch needs: an http.ResponseWriter that keeps what was
// written instead of sending it anywhere.
type recorder struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
}

func (rec *recorder) Header() http.Header {
	if rec.header == nil {
		rec.header = http.Header{}
	}
	return rec.header
}

func (rec *recorder) WriteHeader(code int) {
	if !rec.wrote {
		rec.status, rec.wrote = code, true
	}
}

func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.status, rec.wrote = http.StatusOK, true
	}
	return rec.body.Write(b)
}

// Flush exists so handlers that check for http.Flusher take their streaming
// path's sibling without panicking. Nothing here streams: every tool asks
// for the non-following form of a log.
func (rec *recorder) Flush() {}

// dispatch runs one request through the dashboard's routing tree and
// returns what the handler wrote.
func (s *Server) dispatch(ctx context.Context, method, path string, query url.Values, body []byte) (status int, contentType string, payload []byte, err error) {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, "", nil, fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if creds, ok := ctx.Value(mcpCredsKey{}).(mcpCreds); ok {
		if creds.authorization != "" {
			req.Header.Set("Authorization", creds.authorization)
		}
		if creds.session != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: creds.session})
		}
	}
	// Rate limiting and logging both want a caller; name this one honestly.
	req.RemoteAddr = "mcp:0"

	rec := &recorder{status: http.StatusOK}
	s.routes().ServeHTTP(rec, req)
	return rec.status, rec.Header().Get("Content-Type"), rec.body.Bytes(), nil
}

// register turns a declarative tool into one the protocol layer can run.
func (s *Server) register(t mcpTool) mcp.Tool {
	schema := t.schema
	if schema.Type == "" {
		schema.Type = "object"
	}
	annotations := t.hints
	annotations.Title = t.title

	return mcp.Tool{
		Name:        t.name,
		Title:       t.title,
		Description: t.desc,
		InputSchema: schema,
		Annotations: &annotations,
		Call: func(ctx context.Context, raw json.RawMessage) (*mcp.Result, error) {
			return s.runTool(ctx, t, raw)
		},
	}
}

func (s *Server) runTool(ctx context.Context, t mcpTool, raw json.RawMessage) (*mcp.Result, error) {
	args := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
		}
	}

	// Anything the path, the query string or this layer itself consumes is
	// not also sent in the body.
	consumed := map[string]bool{}

	path, err := fillPath(t.path, args, consumed)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	for _, name := range t.query {
		consumed[name] = true
		raw, ok := args[name]
		if !ok {
			continue
		}
		v, err := scalar(name, raw)
		if err != nil {
			return nil, err
		}
		if v != "" {
			query.Set(name, v)
		}
	}
	for _, name := range t.local {
		consumed[name] = true
	}

	// GET and DELETE carry no body; for the rest, everything left over is
	// the payload, forwarded under the names the handler already expects.
	var body []byte
	if t.method != http.MethodGet && t.method != http.MethodDelete {
		payload := map[string]json.RawMessage{}
		for name, v := range args {
			if consumed[name] {
				continue
			}
			// An argument may be presented under a clearer name than the
			// field the handler reads, which is how a rule keyed by its
			// hostname in the path can still be given a new one.
			if field, ok := t.alias[name]; ok {
				name = field
			}
			payload[name] = v
		}
		if body, err = json.Marshal(payload); err != nil {
			return nil, fmt.Errorf("encoding the arguments: %w", err)
		}
	}

	status, contentType, response, err := s.dispatch(ctx, t.method, path, query, body)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return mcp.Errorf("%s", describeFailure(status, response)), nil
	}
	if t.post != nil {
		response = t.post(args, response)
	}
	return mcp.Text("%s", renderResponse(contentType, response)), nil
}

// fillPath substitutes {name} placeholders from the arguments.
func fillPath(pattern string, args map[string]json.RawMessage, consumed map[string]bool) (string, error) {
	out := pattern
	for {
		open := strings.IndexByte(out, '{')
		if open < 0 {
			return out, nil
		}
		end := strings.IndexByte(out[open:], '}')
		if end < 0 {
			return "", fmt.Errorf("malformed route %q", pattern)
		}
		end += open

		name := out[open+1 : end]
		consumed[name] = true
		raw, ok := args[name]
		if !ok {
			return "", fmt.Errorf("%s is required", name)
		}
		value, err := scalar(name, raw)
		if err != nil {
			return "", err
		}
		if value == "" {
			return "", fmt.Errorf("%s cannot be empty", name)
		}
		// The mux matches on the escaped path and hands handlers the
		// decoded segment, so escaping here is what makes a name with a
		// slash or a space in it arrive intact.
		out = out[:open] + url.PathEscape(value) + out[end+1:]
	}
}

// scalar renders a JSON argument as the string a path or query expects,
// accepting the numbers and booleans a model may send where a string was
// asked for rather than failing over the difference.
func scalar(name string, raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("%s must be a single value, not %T", name, v)
	}
}

// describeFailure turns a handler's error response back into a sentence.
// The dashboard's errors are written to be read by a person, which makes
// them exactly what a model needs to decide what to do next.
func describeFailure(status int, body []byte) string {
	var e errorBody
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		msg := e.Error
		for _, d := range e.Details {
			msg += "\n  - " + d
		}
		return msg
	}
	if text := strings.TrimSpace(string(body)); text != "" {
		return text
	}
	return fmt.Sprintf("the request failed with status %d", status)
}

// maxToolText caps a tool's output. A container log or a large repository
// listing can run to megabytes, and filling a model's context with it helps
// nobody; the tools that can produce that much all take an argument to ask
// for less.
const maxToolText = 60000

func renderResponse(contentType string, body []byte) string {
	text := string(body)
	if strings.Contains(contentType, "json") {
		text = prettyJSON(body)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "OK"
	}
	if len(text) > maxToolText {
		return text[:maxToolText] + fmt.Sprintf(
			"\n\n[truncated: %d of %d bytes shown — narrow the request to see the rest]",
			maxToolText, len(text))
	}
	return text
}

// prettyJSON re-indents a response so a model reads structure rather than
// one long line.
func prettyJSON(body []byte) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(body)
	}
	return string(out)
}

// tailLines keeps the last n elements of a JSON array response, which is how
// a finished build's log is kept to a readable size.
func tailLines(args map[string]json.RawMessage, body []byte) []byte {
	n := 200
	if raw, ok := args["tail"]; ok {
		if v, err := scalar("tail", raw); err == nil && v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				n = parsed
			}
		}
	}

	var lines []json.RawMessage
	if err := json.Unmarshal(body, &lines); err != nil {
		return body
	}
	if len(lines) <= n {
		return body
	}
	out, err := json.Marshal(lines[len(lines)-n:])
	if err != nil {
		return body
	}
	return out
}
