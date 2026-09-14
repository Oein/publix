package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/buildlog"
	"github.com/Oein/publix/internal/dockerapi"
	"github.com/Oein/publix/internal/engine"
	"github.com/Oein/publix/internal/store"
)

// newTestServer builds a fully wired server on a throwaway store, already
// set up with a password, and returns a bearer token for it.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()

	st, err := store.OpenAt(filepath.Join(dir, "publix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(func(set *store.Settings) error {
		set.Auth.PasswordHash, set.Auth.Salt = HashPassword("a test password")
		// Routing is rewritten whenever a project changes, so point it
		// somewhere writable rather than at the real Traefik directory.
		set.TraefikDynamicDir = dir
		set.WorkDir = dir
		set.AppsDomains = []store.AppsDomain{{Domain: "apps.example.com", Default: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	logs, err := buildlog.NewStore(filepath.Join(dir, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	// A Docker daemon is not needed for anything tested here; the client is
	// built so the tools that would use it fail rather than panic.
	docker, _ := dockerapi.New()

	srv := New(Options{
		Store:  st,
		Engine: engine.New(st, docker, logs),
		Docker: docker,
		Assets: Dashboard(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, IssueToken(st.Settings().Auth.SessionKey, time.Hour)
}

// rpc sends one JSON-RPC message to /api/mcp and returns the decoded reply.
func rpc(t *testing.T, srv *Server, token, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/mcp = %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("reply is not JSON: %v\n%s", err, rec.Body.String())
	}
	return out
}

// callTool runs one tool and returns its text output, failing the test if
// the tool reported an error.
func callTool(t *testing.T, srv *Server, token, name string, args map[string]any) string {
	t.Helper()
	text, isErr := tryTool(t, srv, token, name, args)
	if isErr {
		t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

// tryTool runs one tool and reports its output along with whether the tool
// said it failed.
func tryTool(t *testing.T, srv *Server, token, name string, args map[string]any) (string, bool) {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": json.RawMessage(params),
	})
	if err != nil {
		t.Fatal(err)
	}

	out := rpc(t, srv, token, string(msg))
	if rpcErr, bad := out["error"]; bad {
		t.Fatalf("%s was refused by the protocol layer: %v", name, rpcErr)
	}
	result, _ := out["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("%s returned no content: %v", name, result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text, result["isError"] == true
}

// Every tool in the catalogue names a route that has to exist. The tools are
// declarative — a path is a string — so a typo would not fail to compile; it
// would fall through to the dashboard's catch-all and quietly answer with
// HTML. This is the test that makes the declarative form safe.
func TestEveryToolRoutesToARealHandler(t *testing.T) {
	srv, _ := newTestServer(t)
	mux := srv.routes()

	for _, tool := range srv.mcpTools() {
		path := tool.path
		// Fill placeholders with something that is a legal path segment.
		for {
			open := strings.IndexByte(path, '{')
			if open < 0 {
				break
			}
			end := strings.IndexByte(path[open:], '}') + open
			path = path[:open] + "x" + path[end+1:]
		}

		req := httptest.NewRequest(tool.method, path, nil)
		_, pattern := mux.Handler(req)
		if pattern == "" || pattern == "/" {
			t.Errorf("%s: %s %s matches no API route (it would fall through to the dashboard)",
				tool.name, tool.method, tool.path)
			continue
		}
		if !strings.HasPrefix(pattern, tool.method+" ") {
			t.Errorf("%s: %s %s matched %q — the method is wrong",
				tool.name, tool.method, tool.path, pattern)
		}
	}
}

// A tool's schema has to describe the arguments the tool actually uses, or a
// model is guessing at names that will be silently dropped.
func TestToolSchemasCoverTheArgumentsTheyUse(t *testing.T) {
	srv, _ := newTestServer(t)
	seen := map[string]bool{}

	for _, tool := range srv.mcpTools() {
		if seen[tool.name] {
			t.Errorf("%s is registered twice", tool.name)
		}
		seen[tool.name] = true

		if tool.desc == "" {
			t.Errorf("%s has no description; a model has nothing to choose it by", tool.name)
		}

		// Every path placeholder must be a declared, required argument:
		// without it the call cannot be built at all.
		rest := tool.path
		for {
			open := strings.IndexByte(rest, '{')
			if open < 0 {
				break
			}
			end := strings.IndexByte(rest[open:], '}') + open
			name := rest[open+1 : end]
			rest = rest[end+1:]

			if _, ok := tool.schema.Properties[name]; !ok {
				t.Errorf("%s: the path needs %q but the schema does not describe it", tool.name, name)
			}
			if !contains(tool.schema.Required, name) {
				t.Errorf("%s: the path needs %q so it must be required", tool.name, name)
			}
		}

		for _, name := range tool.schema.Required {
			if _, ok := tool.schema.Properties[name]; !ok {
				t.Errorf("%s: %q is required but not described", tool.name, name)
			}
		}
		for _, name := range tool.query {
			if _, ok := tool.schema.Properties[name]; !ok {
				t.Errorf("%s: %q is sent as a query parameter but not described", tool.name, name)
			}
		}
		for _, name := range tool.local {
			if _, ok := tool.schema.Properties[name]; !ok {
				t.Errorf("%s: %q is handled locally but not described", tool.name, name)
			}
		}
		for arg := range tool.alias {
			if _, ok := tool.schema.Properties[arg]; !ok {
				t.Errorf("%s: %q is aliased but not described", tool.name, arg)
			}
		}
	}
}

// The MCP endpoint is the whole dashboard behind one URL. It must not be
// reachable without the credential the dashboard itself requires.
func TestMCPRequiresAuthentication(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated MCP call got %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// A token forged with the wrong key must not work, or the endpoint is open
// to anyone who can guess the shape of the header.
func TestMCPRejectsAForgedToken(t *testing.T) {
	srv, _ := newTestServer(t)
	forged := IssueToken("not the server's signing key", time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+forged)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged token got %d, want 401", rec.Code)
	}
}

func TestInitializeAndListTools(t *testing.T) {
	srv, token := newTestServer(t)

	out := rpc(t, srv, token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	result, _ := out["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "publix" {
		t.Errorf("serverInfo.name = %v, want publix", info["name"])
	}
	if s, _ := result["instructions"].(string); !strings.Contains(s, "list_projects") {
		t.Error("the instructions do not point a client at where to start")
	}

	out = rpc(t, srv, token, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ = out["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != len(srv.mcpTools()) {
		t.Fatalf("listed %d tools, want %d", len(tools), len(srv.mcpTools()))
	}
}

// The notification a client sends after initialize takes no reply, and the
// transport says so with 202 and an empty body.
func TestNotificationsAreAcceptedWithoutABody(t *testing.T) {
	srv, token := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("a notification got %d, want 202", rec.Code)
	}
	if body := rec.Body.String(); strings.TrimSpace(body) != "" {
		t.Errorf("a notification was answered with a body: %s", body)
	}
}

// The point of dispatching through the mux is that a tool is the same thing
// as the dashboard button: create a project with one and the other sees it.
func TestCreateProjectThroughMCPIsVisibleToTheAPI(t *testing.T) {
	srv, token := newTestServer(t)

	callTool(t, srv, token, "create_project", map[string]any{
		"name":    "Billing API",
		"repo":    "acme/billing",
		"branch":  "release",
		"domains": []string{"billing.example.com"},
	})

	projects := srv.store.Projects()
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].Name != "Billing API" {
		t.Errorf("name = %q", projects[0].Name)
	}
	if projects[0].Repo == nil || projects[0].Repo.Branch != "release" {
		t.Errorf("repo = %+v, want the branch carried through", projects[0].Repo)
	}

	// And the tool's own listing agrees.
	text := callTool(t, srv, token, "list_projects", nil)
	if !strings.Contains(text, "Billing API") {
		t.Errorf("list_projects does not show the new project:\n%s", text)
	}
}

// The handlers' validation is the whole reason for dispatching through the
// mux rather than reimplementing anything, so a tool has to inherit it.
func TestToolsInheritHandlerValidation(t *testing.T) {
	srv, token := newTestServer(t)
	callTool(t, srv, token, "create_project", map[string]any{"name": "First", "repo": "acme/first"})
	callTool(t, srv, token, "create_project", map[string]any{"name": "Second", "repo": "acme/second"})

	callTool(t, srv, token, "set_domains", map[string]any{
		"project": "first", "domains": []string{"shared.example.com"},
	})

	// The same hostname on a second project is a silent outage for one of
	// them; the handler refuses it and the tool must report that refusal.
	text, isErr := tryTool(t, srv, token, "set_domains", map[string]any{
		"project": "second", "domains": []string{"shared.example.com"},
	})
	if !isErr {
		t.Fatalf("a conflicting hostname was accepted:\n%s", text)
	}
	if !strings.Contains(text, "already used by") {
		t.Errorf("the handler's explanation was lost:\n%s", text)
	}
}

// A secret sent back empty means "keep what is stored". That rule lives in
// the handler, and a tool that bypassed it would blank every secret on the
// first edit of an unrelated variable.
func TestSetEnvThroughMCPKeepsSecretsItIsNotGiven(t *testing.T) {
	srv, token := newTestServer(t)
	callTool(t, srv, token, "create_project", map[string]any{"name": "App"})

	callTool(t, srv, token, "set_env", map[string]any{
		"project": "app",
		"env": []map[string]any{
			{"key": "API_KEY", "value": "super-secret", "secret": true},
			{"key": "LOG_LEVEL", "value": "info"},
		},
	})

	// An edit made the way a model would make it: the project was read
	// back first, so the secret's value came back redacted and empty.
	callTool(t, srv, token, "set_env", map[string]any{
		"project": "app",
		"env": []map[string]any{
			{"key": "API_KEY", "value": "", "secret": true},
			{"key": "LOG_LEVEL", "value": "debug"},
		},
	})

	p, ok := srv.store.Project("app")
	if !ok {
		t.Fatal("the project disappeared")
	}
	found := map[string]string{}
	for _, e := range p.Env {
		found[e.Key] = e.Value
	}
	if found["API_KEY"] != "super-secret" {
		t.Errorf("API_KEY = %q, want the stored secret kept", found["API_KEY"])
	}
	if found["LOG_LEVEL"] != "debug" {
		t.Errorf("LOG_LEVEL = %q, want the edit applied", found["LOG_LEVEL"])
	}
}

// Secret values never leave the server, and the MCP surface must not be the
// hole in that: a model reading a project back gets the same redaction the
// dashboard does.
func TestSecretsAreNotReadableThroughMCP(t *testing.T) {
	srv, token := newTestServer(t)
	callTool(t, srv, token, "create_project", map[string]any{"name": "App"})
	callTool(t, srv, token, "set_env", map[string]any{
		"project": "app",
		"env":     []map[string]any{{"key": "API_KEY", "value": "super-secret", "secret": true}},
	})

	for _, tool := range []string{"get_project", "list_projects"} {
		args := map[string]any{"project": "app"}
		if tool == "list_projects" {
			args = nil
		}
		if text := callTool(t, srv, token, tool, args); strings.Contains(text, "super-secret") {
			t.Errorf("%s leaked a secret value:\n%s", tool, text)
		}
	}
}

// A failed call has to come back as a readable sentence, because that
// sentence is all a model has to decide what to do next.
func TestAMissingProjectIsReportedAsAToolError(t *testing.T) {
	srv, token := newTestServer(t)
	text, isErr := tryTool(t, srv, token, "get_project", map[string]any{"project": "nope"})
	if !isErr {
		t.Fatalf("an unknown project was not an error:\n%s", text)
	}
	if !strings.Contains(text, "nope") {
		t.Errorf("the message does not say what was not found:\n%s", text)
	}
}

// A required argument that was not supplied must be named, not produce a
// request to a nonsense path.
func TestAMissingRequiredArgumentIsNamed(t *testing.T) {
	srv, token := newTestServer(t)
	text, isErr := tryTool(t, srv, token, "get_project", map[string]any{})
	if !isErr {
		t.Fatalf("a call with no project was accepted:\n%s", text)
	}
	if !strings.Contains(text, "project") {
		t.Errorf("the message does not name the missing argument:\n%s", text)
	}
}

// Arguments that the handlers do not know are rejected rather than ignored,
// which is what stops a model from believing a setting took effect.
func TestAnUnknownArgumentIsRefused(t *testing.T) {
	srv, token := newTestServer(t)
	callTool(t, srv, token, "create_project", map[string]any{"name": "App"})

	text, isErr := tryTool(t, srv, token, "update_project", map[string]any{
		"project": "app", "autoDeployment": true, // no such field
	})
	if !isErr {
		t.Fatalf("an unknown field was silently accepted:\n%s", text)
	}
}

// Settings tools reach the same store the dashboard writes, and a rule keyed
// by hostname can be moved to a different one.
func TestProxyRulesThroughMCP(t *testing.T) {
	srv, token := newTestServer(t)

	callTool(t, srv, token, "add_proxy", map[string]any{
		"domain": "legacy.example.com",
		"target": "http://127.0.0.1:8080",
	})
	if got := srv.store.Settings().Proxies; len(got) != 1 || got[0].Domain != "legacy.example.com" {
		t.Fatalf("proxies = %+v", got)
	}

	callTool(t, srv, token, "update_proxy", map[string]any{
		"domain":    "legacy.example.com",
		"newDomain": "old.example.com",
		"target":    "http://127.0.0.1:9090",
	})
	got := srv.store.Settings().Proxies
	if len(got) != 1 {
		t.Fatalf("got %d proxies, want the rule replaced not duplicated: %+v", len(got), got)
	}
	if got[0].Domain != "old.example.com" {
		t.Errorf("domain = %q, want the rule moved to the new hostname", got[0].Domain)
	}
	if got[0].Target != "http://127.0.0.1:9090" {
		t.Errorf("target = %q", got[0].Target)
	}

	callTool(t, srv, token, "delete_proxy", map[string]any{"domain": "old.example.com"})
	if got := srv.store.Settings().Proxies; len(got) != 0 {
		t.Errorf("proxies = %+v, want none", got)
	}
}

// A model may send a number where a string was described. Refusing over the
// difference would be pedantry; the value is unambiguous either way.
func TestScalarArgumentsAcceptTheTypesAModelSends(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"main"`, "main"},
		{`42`, "42"},
		{`true`, "true"},
		{`null`, ""},
	} {
		got, err := scalar("ref", json.RawMessage(tc.raw))
		if err != nil {
			t.Errorf("scalar(%s): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("scalar(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	if _, err := scalar("ref", json.RawMessage(`{"a":1}`)); err == nil {
		t.Error("an object was accepted where a single value was needed")
	}
}

// A path argument carrying a slash must not be able to reach a different
// route than the one the tool declares.
func TestPathArgumentsAreEscaped(t *testing.T) {
	consumed := map[string]bool{}
	got, err := fillPath("/api/projects/{project}", map[string]json.RawMessage{
		"project": json.RawMessage(`"../../api/settings"`),
	}, consumed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "/api/settings") {
		t.Errorf("a path argument escaped its segment: %s", got)
	}
	if !consumed["project"] {
		t.Error("a path argument was not marked consumed, so it would also be sent in the body")
	}
}

// Build logs can run to megabytes. The tail argument has to actually reduce
// what comes back, or the cap truncates from the wrong end and the error
// that explains the failure is the part that gets dropped.
func TestTailLinesKeepsTheEnd(t *testing.T) {
	lines := make([]map[string]any, 0, 500)
	for i := range 500 {
		lines = append(lines, map[string]any{"seq": i, "text": "line"})
	}
	body, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}

	out := tailLines(map[string]json.RawMessage{"tail": json.RawMessage(`5`)}, body)
	var kept []map[string]any
	if err := json.Unmarshal(out, &kept); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 5 {
		t.Fatalf("kept %d lines, want 5", len(kept))
	}
	if kept[len(kept)-1]["seq"].(float64) != 499 {
		t.Errorf("the last line is %v, want the end of the log", kept[len(kept)-1]["seq"])
	}
}

// A response longer than the cap is trimmed with a note saying so, rather
// than ending mid-sentence as though that were all there was.
func TestOversizedResponsesSayTheyWereTruncated(t *testing.T) {
	huge := `"` + strings.Repeat("x", maxToolText+1000) + `"`
	out := renderResponse("application/json", []byte(huge))
	if len(out) <= maxToolText {
		t.Fatalf("output is %d bytes, want the cap applied", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Error("the output was cut without saying so")
	}
}

// The transport has no server-to-client stream, and a client opening one
// should be told rather than left holding a connection open.
func TestGetOnTheMCPEndpointIsRefused(t *testing.T) {
	srv, token := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/mcp = %d, want 405", rec.Code)
	}
}

// The dashboard and the MCP layer have to be the same routing tree, or a
// tool could be dispatched against routes the server does not actually
// serve.
func TestHandlerAndToolsShareOneRoutingTree(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.routes() != srv.routes() {
		t.Error("routes() built a second mux")
	}
}

// Reading a request body is what several handlers do first; the recorder
// has to report what they wrote even when nothing set a status explicitly.
func TestRecorderDefaultsToOK(t *testing.T) {
	rec := &recorder{status: http.StatusOK}
	if _, err := io.Copy(rec, bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.status)
	}
	if rec.body.String() != "hello" {
		t.Errorf("body = %q", rec.body.String())
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
