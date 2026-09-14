package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Oein/publix/internal/api"
	"github.com/Oein/publix/internal/store"
)

// tokenTTL is how long a minted bearer token lasts. Long enough that an
// editor's MCP configuration is not a weekly chore, short enough that a
// token left in a dotfile does not outlive the machine it is on.
const tokenTTL = 90 * 24 * time.Hour

// cmdToken mints a bearer token for scripted and MCP clients.
//
// It reads the store directly rather than logging in, because the person
// running it is already on the server with access to the state file — and
// because it has to work when the dashboard is what is broken.
func cmdToken(ctx context.Context, args []string) error {
	fs := flagSet("token")
	ttl := fs.Duration("ttl", tokenTTL, "how long the token stays valid")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}

	token, err := mintToken(*ttl)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}

func mintToken(ttl time.Duration) (string, error) {
	st, err := store.Open()
	if err != nil {
		return "", err
	}
	set := st.Settings()
	if set.Auth.PasswordHash == "" {
		return "", fmt.Errorf("this server has not been set up yet — open the dashboard and choose a password first")
	}
	if set.Auth.SessionKey == "" {
		return "", fmt.Errorf("this server has no session key; open the dashboard and sign in once")
	}
	return api.IssueToken(set.Auth.SessionKey, ttl), nil
}

// cmdMCP bridges a Model Context Protocol client to the running server.
//
// The protocol is the same either way — JSON-RPC 2.0 messages — so this is
// only a pump: read a message from stdin, POST it to /api/mcp, write the
// reply to stdout. Keeping the tools themselves in the server is what makes
// them available to a remote client over HTTP as well, and means this
// command has nothing to keep in step.
func cmdMCP(ctx context.Context, args []string) error {
	fs := flagSet("mcp")
	server := fs.String("url", envOr("PUBLIX_URL", "http://127.0.0.1:4321"), "the publix server to talk to")
	token := fs.String("token", os.Getenv("PUBLIX_TOKEN"), "bearer token; defaults to one minted from the local store")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}

	base, err := url.Parse(strings.TrimRight(*server, "/"))
	if err != nil || base.Host == "" {
		return fmt.Errorf("%q is not a valid server URL", *server)
	}
	endpoint := base.String() + "/api/mcp"

	if *token == "" {
		// Minting from the local store only makes sense for the local
		// server: the token is signed with that store's key and no other
		// server would accept it.
		if !isLoopback(base.Host) {
			return fmt.Errorf("a token is required for a remote server\n\nRun `publix token` on %s and pass it with -token, or set PUBLIX_TOKEN.", base.Host)
		}
		if *token, err = mintToken(tokenTTL); err != nil {
			return err
		}
	}

	client := &http.Client{
		// A tool may start a deploy or reach GitHub, neither of which is
		// instant; but a request that has hung must eventually give the
		// client an answer rather than block the session forever.
		Timeout: 5 * time.Minute,
	}

	in := bufio.NewScanner(os.Stdin)
	// One message per line, and a message carrying a repository's
	// environment can be large.
	in.Buffer(make([]byte, 0, 64*1024), 16<<20)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		// Scan reuses its buffer, and the request outlives this iteration
		// only if we copy.
		msg := append([]byte(nil), line...)

		reply, err := forward(ctx, client, endpoint, *token, msg)
		if err != nil {
			// Never leave a client waiting on a reply that will not come:
			// answer in the protocol, so the failure is something it can
			// show rather than a hang.
			reply = rpcFailure(msg, err)
		}
		if reply == nil {
			continue // a notification; the specification says stay silent
		}
		if _, err := out.Write(append(reply, '\n')); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
	if err := in.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("reading from the client: %w", err)
	}
	return nil
}

// forward sends one message and returns the reply, or nil when the server
// accepted it without one.
func forward(ctx context.Context, client *http.Client, endpoint, token string, msg []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(msg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach publix at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}

	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("publix rejected the token — mint a fresh one with `publix token` (changing the dashboard password revokes every token)")
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("this publix server has no /api/mcp endpoint; it is older than the MCP support in this client")
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("publix returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	return bytes.TrimSpace(body), nil
}

// rpcFailure builds a JSON-RPC error response addressed to the request that
// failed, so the client can match it to what it sent.
func rpcFailure(request []byte, cause error) []byte {
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(request, &envelope)
	if len(envelope.ID) == 0 || string(envelope.ID) == "null" {
		// It was a notification. There is nobody waiting for an answer, so
		// report it where a stdio client shows diagnostics.
		fmt.Fprintf(os.Stderr, "publix mcp: %v\n", cause)
		return nil
	}

	reply, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      envelope.ID,
		"error":   map[string]any{"code": -32603, "message": cause.Error()},
	})
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error"}}`)
	}
	return reply
}

func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
