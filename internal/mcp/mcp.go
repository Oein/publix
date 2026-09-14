// Package mcp implements the server half of the Model Context Protocol.
//
// It is deliberately small and depends on nothing outside the standard
// library, which is the same bargain the rest of publix makes. The protocol
// is JSON-RPC 2.0 carrying a handful of methods; the parts publix needs are
// initialize, tools/list and tools/call.
//
// The package is transport-agnostic: Handle takes one encoded message and
// returns the encoded reply, or nil when the message was a notification and
// the specification says to stay silent. publix serves it over HTTP at
// /api/mcp, and `publix mcp` pumps the same messages over stdio.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// Protocol revisions this server speaks, newest first. A client that asks
// for one of these is given it back; anything else negotiates down to the
// newest, which is how the specification says to propose an alternative.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes, from the JSON-RPC 2.0 specification.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Tool is one capability offered to a client.
type Tool struct {
	// Name is what tools/call refers to. It is the stable identifier, so
	// renaming one breaks anything that has it written down.
	Name string
	// Title is the human label a client may show instead of Name.
	Title string
	// Description is what the model reads to decide whether this is the
	// tool it wants. It is the single most important field here.
	Description string
	InputSchema Schema
	Annotations *Annotations

	// Call runs the tool. Returning an error reports a failed *tool*, not
	// a failed protocol call: the client sees it as a tool result marked
	// isError, which is what lets a model read the message and try again.
	Call func(ctx context.Context, arguments json.RawMessage) (*Result, error)
}

// Schema is the JSON Schema describing a tool's arguments. MCP requires the
// top level to be an object.
type Schema struct {
	Type string `json:"type"`
	// Properties carries no omitempty: a tool that takes no arguments must
	// still publish an empty object, because several clients treat a
	// missing properties map as a malformed schema.
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property is one argument's schema. It carries the subset of JSON Schema
// that publix's tools actually need.
type Property struct {
	Type        string              `json:"type,omitempty"`
	Description string              `json:"description,omitempty"`
	Enum        []string            `json:"enum,omitempty"`
	Items       *Property           `json:"items,omitempty"`
	Properties  map[string]Property `json:"properties,omitempty"`
	Required    []string            `json:"required,omitempty"`
	Default     any                 `json:"default,omitempty"`
	Minimum     *float64            `json:"minimum,omitempty"`
	Maximum     *float64            `json:"maximum,omitempty"`
}

// Annotations are hints about what a tool does. They are advisory — a
// client may show a stronger confirmation for something destructive — and
// are not a security boundary.
type Annotations struct {
	Title string `json:"title,omitempty"`
	// ReadOnlyHint says the tool changes nothing.
	ReadOnlyHint bool `json:"readOnlyHint,omitempty"`
	// DestructiveHint says the tool may remove or overwrite something. It
	// is a pointer because the protocol's default is true, so "not
	// destructive" has to be said explicitly.
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	// IdempotentHint says repeating the call with the same arguments has
	// no further effect.
	IdempotentHint bool `json:"idempotentHint,omitempty"`
	// OpenWorldHint says the tool touches something outside this server.
	OpenWorldHint *bool `json:"openWorldHint,omitempty"`
}

// Result is what a tool returns.
type Result struct {
	Content []Content `json:"content"`
	// IsError marks a tool that ran but failed. The model is expected to
	// read Content and decide what to do, so the message matters.
	IsError bool `json:"isError,omitempty"`
}

// Content is one piece of a tool's output. publix only ever returns text.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Text builds a plain text result.
func Text(format string, args ...any) *Result {
	return &Result{Content: []Content{{Type: "text", Text: fmt.Sprintf(format, args...)}}}
}

// Errorf builds a result marked as a tool failure.
func Errorf(format string, args ...any) *Result {
	r := Text(format, args...)
	r.IsError = true
	return r
}

// Server dispatches protocol messages against a set of tools.
type Server struct {
	name         string
	version      string
	instructions string

	tools []Tool
	index map[string]int
}

// NewServer creates a server. Instructions are handed to the client at
// initialize and describe, in prose, what this server is for.
func NewServer(name, version, instructions string) *Server {
	return &Server{
		name:         name,
		version:      version,
		instructions: instructions,
		index:        map[string]int{},
	}
}

// Register adds a tool, replacing any earlier tool of the same name.
func (s *Server) Register(t Tool) {
	if i, ok := s.index[t.Name]; ok {
		s.tools[i] = t
		return
	}
	s.index[t.Name] = len(s.tools)
	s.tools = append(s.tools, t)
}

// Tools returns the registered tools in registration order, which is the
// order a client lists them in. Grouping related tools together is the only
// structure a flat list has.
func (s *Server) Tools() []Tool { return s.tools }

// Wire types. Success and failure are separate structs because JSON-RPC
// requires exactly one of "result" and "error" to be present, and a single
// struct with omitempty gets that wrong for empty results such as ping's.

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type okResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type errResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Handle processes one encoded message and returns the encoded reply, or
// nil when no reply is due — which is the case for notifications, and for a
// batch made only of notifications.
func (s *Server) Handle(ctx context.Context, raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return encode(errResponse{"2.0", nil, &rpcError{CodeParse, "empty message", nil}})
	}

	// Batches were removed in the 2025-06-18 revision but older clients
	// still send them, and answering costs almost nothing.
	if raw[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(raw, &batch); err != nil {
			return encode(errResponse{"2.0", nil, &rpcError{CodeParse, err.Error(), nil}})
		}
		replies := make([]json.RawMessage, 0, len(batch))
		for _, one := range batch {
			if reply := s.Handle(ctx, one); reply != nil {
				replies = append(replies, reply)
			}
		}
		if len(replies) == 0 {
			return nil
		}
		return encode(replies)
	}

	var msg message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return encode(errResponse{"2.0", nil, &rpcError{CodeParse, err.Error(), nil}})
	}

	// A message without an id is a notification: it is answered with
	// silence however it turns out.
	notification := len(msg.ID) == 0 || string(msg.ID) == "null"

	result, rerr := s.call(ctx, msg.Method, msg.Params)
	if notification {
		return nil
	}
	if rerr != nil {
		return encode(errResponse{"2.0", msg.ID, rerr})
	}
	return encode(okResponse{"2.0", msg.ID, result})
}

func (s *Server) call(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.initialize(params), nil

	case "ping":
		return struct{}{}, nil

	case "tools/list":
		return map[string]any{"tools": s.describe()}, nil

	case "tools/call":
		return s.callTool(ctx, params)

	case "notifications/initialized", "notifications/cancelled", "notifications/progress":
		// Nothing to do, and nothing to say: these arrive as
		// notifications, so the reply is discarded either way.
		return struct{}{}, nil

	default:
		return nil, &rpcError{CodeMethodNotFound, fmt.Sprintf("this server does not implement %q", method), nil}
	}
}

func (s *Server) initialize(params json.RawMessage) any {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &in)

	// Agree to the client's revision when it is one we speak, otherwise
	// name ours and let the client decide whether it can continue.
	version := protocolVersions[0]
	for _, v := range protocolVersions {
		if v == in.ProtocolVersion {
			version = v
			break
		}
	}

	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"title":   s.name,
			"version": s.version,
		},
		"instructions": s.instructions,
	}
}

// toolDescription is a Tool as it appears on the wire — everything but the
// function that runs it.
type toolDescription struct {
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	InputSchema Schema       `json:"inputSchema"`
	Annotations *Annotations `json:"annotations,omitempty"`
}

func (s *Server) describe() []toolDescription {
	out := make([]toolDescription, 0, len(s.tools))
	for _, t := range s.tools {
		schema := t.InputSchema
		if schema.Type == "" {
			schema.Type = "object"
		}
		if schema.Properties == nil {
			// An absent properties map is legal but several clients choke
			// on it; an empty object says the same thing safely.
			schema.Properties = map[string]Property{}
		}
		out = append(out, toolDescription{
			Name:        t.Name,
			Title:       t.Title,
			Description: t.Description,
			InputSchema: schema,
			Annotations: t.Annotations,
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (result any, rerr *rpcError) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, &rpcError{CodeInvalidParams, err.Error(), nil}
	}

	i, ok := s.index[in.Name]
	if !ok {
		return nil, &rpcError{CodeInvalidParams, fmt.Sprintf("there is no tool named %q", in.Name), nil}
	}
	tool := s.tools[i]

	// A panic in one tool must not take down the connection and every
	// other tool with it.
	defer func() {
		if rv := recover(); rv != nil {
			result, rerr = Errorf("%s failed unexpectedly: %v", in.Name, rv), nil
		}
	}()

	out, err := tool.Call(ctx, in.Arguments)
	if err != nil {
		// A tool that failed is a result, not a protocol error: the model
		// should see the message and be able to act on it.
		return Errorf("%s", err.Error()), nil
	}
	if out == nil {
		out = Text("OK")
	}
	return out, nil
}

func encode(v any) []byte {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// Encoding a response cannot fail for any value this package
		// builds, but a tool returning something unmarshalable would end
		// up here, and silence would be worse than a generic error.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"could not encode the response"}}` + "\n")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
