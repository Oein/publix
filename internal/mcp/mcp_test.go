package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testServer() *Server {
	s := NewServer("test", "1.0", "instructions")
	s.Register(Tool{
		Name:        "echo",
		Description: "echo back",
		Call: func(ctx context.Context, args json.RawMessage) (*Result, error) {
			return Text("got %s", string(args)), nil
		},
	})
	s.Register(Tool{
		Name: "boom",
		Call: func(ctx context.Context, args json.RawMessage) (*Result, error) {
			return nil, errors.New("the thing went wrong")
		},
	})
	s.Register(Tool{
		Name: "panics",
		Call: func(ctx context.Context, args json.RawMessage) (*Result, error) {
			panic("not today")
		},
	})
	return s
}

// decode unpacks a reply into the loose shape a test wants to poke at.
func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if raw == nil {
		t.Fatal("no reply at all")
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("reply is not JSON: %v\n%s", err, raw)
	}
	return out
}

func call(t *testing.T, s *Server, msg string) map[string]any {
	t.Helper()
	return decode(t, s.Handle(context.Background(), []byte(msg)))
}

// A notification carries no id, and the specification is explicit that it
// gets no reply. Answering one corrupts a stdio stream, because the client
// is not reading for a response.
func TestNotificationsAreAnsweredWithSilence(t *testing.T) {
	s := testServer()
	for _, msg := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"notifications/initialized"}`,
	} {
		if reply := s.Handle(context.Background(), []byte(msg)); reply != nil {
			t.Errorf("a notification was answered with %s", reply)
		}
	}
}

// ping's result is an empty object. It has to survive encoding as one: a
// response with neither result nor error is not valid JSON-RPC, and a
// client waiting on the keepalive would see a malformed message.
func TestPingCarriesAnEmptyResultRatherThanNone(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if _, ok := out["result"]; !ok {
		t.Errorf("ping answered without a result field: %v", out)
	}
	if _, ok := out["error"]; ok {
		t.Errorf("ping answered with an error: %v", out)
	}
}

func TestInitializeAgreesToAVersionItSpeaks(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	result, _ := out["result"].(map[string]any)
	if got := result["protocolVersion"]; got != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want the client's own revision back", got)
	}
}

// A revision we do not know must not be echoed back as though we did: the
// answer is to name ours and let the client decide.
func TestInitializeProposesItsOwnVersionForAnUnknownOne(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	result, _ := out["result"].(map[string]any)
	if got := result["protocolVersion"]; got != protocolVersions[0] {
		t.Errorf("protocolVersion = %v, want %q", got, protocolVersions[0])
	}
}

func TestToolsListDescribesEveryRegisteredTool(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, _ := out["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("listed %d tools, want 3", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	// An absent properties map trips several clients up, so the schema is
	// always filled in even for a tool that takes nothing.
	schema, _ := first["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("inputSchema.type = %v, want object", schema["type"])
	}
	if _, ok := schema["properties"]; !ok {
		t.Error("inputSchema has no properties map")
	}
}

// A tool that fails is a result the model can read and act on, not a
// protocol error — a protocol error tells the client the call was malformed,
// which is a different thing and not recoverable by trying again.
func TestAFailingToolIsAResultNotAProtocolError(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom"}}`)
	if _, bad := out["error"]; bad {
		t.Fatalf("a failing tool was reported as a protocol error: %v", out)
	}
	result, _ := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("the result is not marked isError: %v", result)
	}
	if !strings.Contains(encodeString(result), "the thing went wrong") {
		t.Errorf("the tool's message was lost: %v", result)
	}
}

// One tool panicking must not take down the connection and every other tool
// with it.
func TestAPanickingToolIsContained(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"panics"}}`)
	result, _ := out["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a panic did not surface as a tool error: %v", out)
	}
	if !strings.Contains(encodeString(result), "not today") {
		t.Errorf("the panic value was not reported: %v", result)
	}
}

func TestCallingAToolThatDoesNotExistIsAProtocolError(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope"}}`)
	rpcErr, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("an unknown tool was accepted: %v", out)
	}
	if code := rpcErr["code"].(float64); int(code) != CodeInvalidParams {
		t.Errorf("code = %v, want %d", code, CodeInvalidParams)
	}
}

func TestAnUnknownMethodSaysSo(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	rpcErr, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("an unimplemented method was accepted: %v", out)
	}
	if code := rpcErr["code"].(float64); int(code) != CodeMethodNotFound {
		t.Errorf("code = %v, want %d", code, CodeMethodNotFound)
	}
}

func TestMalformedJSONIsAParseError(t *testing.T) {
	out := call(t, testServer(), `{"jsonrpc":"2.0",`)
	rpcErr, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("broken JSON was accepted: %v", out)
	}
	if code := rpcErr["code"].(float64); int(code) != CodeParse {
		t.Errorf("code = %v, want %d", code, CodeParse)
	}
}

// Batches were dropped from the specification but older clients still send
// them, and each element has to be answered in place.
func TestABatchIsAnsweredElementByElement(t *testing.T) {
	s := testServer()
	reply := s.Handle(context.Background(), []byte(
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},`+
			`{"jsonrpc":"2.0","method":"notifications/initialized"},`+
			`{"jsonrpc":"2.0","id":2,"method":"ping"}]`))

	var replies []map[string]any
	if err := json.Unmarshal(reply, &replies); err != nil {
		t.Fatalf("a batch was not answered with an array: %v\n%s", err, reply)
	}
	// The notification in the middle contributes nothing.
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2: %s", len(replies), reply)
	}
}

// A batch of nothing but notifications is answered with silence, the same
// way each of them would be alone.
func TestABatchOfNotificationsIsSilent(t *testing.T) {
	s := testServer()
	reply := s.Handle(context.Background(), []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
	if reply != nil {
		t.Errorf("a batch of notifications was answered with %s", reply)
	}
}

// Registering the same name twice replaces rather than duplicating, so a
// client never sees two tools it cannot tell apart.
func TestRegisteringTheSameNameReplacesIt(t *testing.T) {
	s := NewServer("test", "1.0", "")
	s.Register(Tool{Name: "one", Description: "first"})
	s.Register(Tool{Name: "one", Description: "second"})
	if len(s.Tools()) != 1 {
		t.Fatalf("got %d tools, want 1", len(s.Tools()))
	}
	if s.Tools()[0].Description != "second" {
		t.Errorf("description = %q, want the later registration to win", s.Tools()[0].Description)
	}
}

func encodeString(v any) string {
	out, _ := json.Marshal(v)
	return string(out)
}
