// Package dockerapi is a small, dependency-free client for the Docker Engine
// HTTP API. publix only needs a narrow slice of the API, and talking to the
// socket directly keeps the binary tiny and the behaviour predictable.
package dockerapi

import (
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
)

// DefaultVersion is the Engine API version publix negotiates against. 1.41
// ships with Docker 20.10 (2020), which is old enough to be everywhere.
const DefaultVersion = "1.41"

// Client talks to a Docker daemon over a unix socket or TCP endpoint.
type Client struct {
	http    *http.Client
	base    string // scheme://host prefix for requests
	version string
	host    string
}

// New connects to the daemon named by DOCKER_HOST, falling back to the
// standard unix socket.
func New() (*Client, error) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	return NewWithHost(host)
}

// NewWithHost connects to an explicit daemon address.
func NewWithHost(host string) (*Client, error) {
	c := &Client{version: DefaultVersion, host: host}

	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("cannot reach the Docker socket at %s: %w\n\nIs Docker running, and is your user in the `docker` group?", path, err)
		}
		c.base = "http://docker"
		c.http = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", path)
				},
				DisableCompression: true,
			},
		}
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"):
		c.base = "http://" + strings.TrimPrefix(strings.TrimPrefix(host, "tcp://"), "http://")
		c.http = &http.Client{Transport: &http.Transport{DisableCompression: true}}
	default:
		return nil, fmt.Errorf("unsupported DOCKER_HOST %q (expected unix:// or tcp://)", host)
	}
	return c, nil
}

// Host reports the endpoint this client is bound to.
func (c *Client) Host() string { return c.host }

func (c *Client) url(path string, q url.Values) string {
	u := fmt.Sprintf("%s/v%s%s", c.base, c.version, path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// APIError is a non-2xx response from the daemon.
type APIError struct {
	Status  int
	Message string
	Path    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("docker api %s: %d %s", e.Path, e.Status, e.Message)
}

// NotFound reports whether err is a 404 from the daemon.
func NotFound(err error) bool {
	var ae *APIError
	if ok := asAPIError(err, &ae); ok {
		return ae.Status == http.StatusNotFound
	}
	return false
}

// Conflict reports whether err is a 409 from the daemon (e.g. name in use).
func Conflict(err error) bool {
	var ae *APIError
	if ok := asAPIError(err, &ae); ok {
		return ae.Status == http.StatusConflict
	}
	return false
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if ae, ok := err.(*APIError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rdr io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rdr, contentType = b, "application/x-tar"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		rdr, contentType = bytes.NewReader(raw), "application/json"
	}

	req, err := http.NewRequestWithContext(ctx, method, c.url(path, q), rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker api %s: %w", path, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		msg := strings.TrimSpace(string(raw))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg, Path: path}
	}
	return resp, nil
}

// getJSON performs a GET and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err := io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// postJSON performs a POST and optionally decodes the JSON body into out.
func (c *Client) postJSON(ctx context.Context, path string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, http.MethodPost, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err := io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Ping verifies the daemon is reachable and returns its reported version.
func (c *Client) Ping(ctx context.Context) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var v Version
	err := c.getJSON(ctx, "/version", nil, &v)
	return v, err
}

// filterArgs builds the JSON-encoded `filters` query parameter.
func filterArgs(kv map[string][]string) string {
	raw, _ := json.Marshal(kv)
	return string(raw)
}
