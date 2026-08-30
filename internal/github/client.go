// Package github talks to the GitHub REST API.
//
// Two credential styles are supported because they suit different setups:
// a personal access token is the fastest way to get a self-hosted install
// working, and a GitHub App is what an organisation needs — it can be
// scoped per repository, its tokens rotate, and it can create the webhooks
// publix relies on without anyone's personal account being involved.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Oein/publix/internal/store"
)

// DefaultAPIBase is the public GitHub API root.
const DefaultAPIBase = "https://api.github.com"

// Client is an authenticated GitHub API client.
type Client struct {
	http    *http.Client
	base    string
	auth    authenticator
	mu      sync.Mutex
	rateLog time.Time
}

// authenticator supplies the Authorization header for each request.
type authenticator interface {
	token(ctx context.Context, c *Client) (string, error)
	scheme() string
}

// New builds a client from the platform's stored GitHub settings.
func New(set store.GitHubSettings) (*Client, error) {
	base := strings.TrimSuffix(firstNonEmpty(set.APIBase, DefaultAPIBase), "/")
	c := &Client{
		http: &http.Client{Timeout: 30 * time.Second},
		base: base,
	}

	switch {
	case set.AppID != "" && set.PrivateKey != "":
		key, err := parsePrivateKey(set.PrivateKey)
		if err != nil {
			return nil, err
		}
		c.auth = &appAuth{appID: set.AppID, installationID: set.InstallationID, key: key}
	case set.Token != "":
		c.auth = &tokenAuth{pat: set.Token}
	default:
		return nil, ErrNotConfigured
	}
	return c, nil
}

// ErrNotConfigured is returned when no GitHub credentials are set up.
var ErrNotConfigured = fmt.Errorf("GitHub is not connected: add a personal access token or a GitHub App under Settings → GitHub")

// tokenAuth authenticates with a personal access token.
type tokenAuth struct{ pat string }

func (a *tokenAuth) token(context.Context, *Client) (string, error) { return a.pat, nil }
func (a *tokenAuth) scheme() string                                 { return "Bearer" }

// appAuth authenticates as a GitHub App installation, minting and caching
// installation tokens as they expire.
type appAuth struct {
	appID          string
	installationID string
	key            *rsa.PrivateKey

	mu      sync.Mutex
	cached  string
	expires time.Time
}

func (a *appAuth) scheme() string { return "Bearer" }

func (a *appAuth) token(ctx context.Context, c *Client) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Refresh a minute early: a token that expires mid-request produces a
	// confusing 401 on an operation that had nothing wrong with it.
	if a.cached != "" && time.Now().Before(a.expires.Add(-time.Minute)) {
		return a.cached, nil
	}

	jwt, err := a.appJWT()
	if err != nil {
		return "", err
	}
	installation := a.installationID
	if installation == "" {
		if installation, err = a.discoverInstallation(ctx, c, jwt); err != nil {
			return "", err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/app/installations/%s/access_tokens", c.base, url.PathEscape(installation)), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", apiError(resp, "minting an installation token")
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	a.cached, a.expires = out.Token, out.ExpiresAt
	return a.cached, nil
}

// discoverInstallation finds the App's single installation, so an operator
// who pasted an App ID and key does not also have to hunt for the numeric
// installation ID.
func (a *appAuth) discoverInstallation(ctx context.Context, c *Client, jwt string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/app/installations", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", apiError(resp, "listing app installations")
	}
	var out []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	switch len(out) {
	case 0:
		return "", fmt.Errorf("this GitHub App has no installations yet — install it on the account or organisation whose repositories you want to deploy")
	case 1:
		return strconv.FormatInt(out[0].ID, 10), nil
	default:
		names := make([]string, 0, len(out))
		for _, i := range out {
			names = append(names, fmt.Sprintf("%s (%d)", i.Account.Login, i.ID))
		}
		return "", fmt.Errorf("this GitHub App has %d installations; set the installation ID explicitly. Available: %s",
			len(out), strings.Join(names, ", "))
	}
}

// appJWT signs the short-lived assertion used to mint installation tokens.
func (a *appAuth) appJWT() (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		// Backdate slightly: GitHub rejects a token whose iat is in the
		// future, and a second of clock skew is common.
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.appID,
	}

	enc := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(raw), nil
	}
	h, err := enc(header)
	if err != nil {
		return "", err
	}
	c, err := enc(claims)
	if err != nil {
		return "", err
	}

	signing := h + "." + c
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parsePrivateKey reads a PEM-encoded RSA key in either PKCS#1 or PKCS#8
// form, which is what GitHub hands out depending on how it was downloaded.
func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemData)))
	if block == nil {
		return nil, fmt.Errorf("the GitHub App private key is not valid PEM (it should begin with -----BEGIN RSA PRIVATE KEY-----)")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App private key could not be parsed: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the GitHub App private key must be an RSA key, got %T", parsed)
	}
	return key, nil
}

// do performs an authenticated API request.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}

	u := path
	if !strings.HasPrefix(path, "http") {
		u = c.base + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}

	tok, err := c.auth.token(ctx, c)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.auth.scheme()+" "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "publix")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling GitHub: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, apiError(resp, method+" "+path)
	}
	if out == nil {
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp, nil
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, fmt.Errorf("reading GitHub's response to %s: %w", path, err)
	}
	return resp, nil
}

// Error is a failed GitHub API call.
type Error struct {
	Status  int
	Message string
	Op      string
	// RateLimited marks the specific case worth handling differently.
	RateLimited bool
	ResetAt     time.Time
}

func (e *Error) Error() string {
	switch {
	case e.RateLimited:
		return fmt.Sprintf("GitHub rate limit exceeded; it resets at %s", e.ResetAt.Format(time.Kitchen))
	case e.Status == http.StatusUnauthorized:
		return "GitHub rejected the credentials: check the token or App key under Settings → GitHub"
	case e.Status == http.StatusForbidden:
		return fmt.Sprintf("GitHub refused the request (%s). The token may be missing the `repo` scope, or the App may not be installed on that repository.", e.Message)
	case e.Status == http.StatusNotFound:
		return fmt.Sprintf("GitHub could not find it (%s). For a private repository this usually means the credentials cannot see it.", e.Op)
	default:
		return fmt.Sprintf("GitHub returned %d for %s: %s", e.Status, e.Op, e.Message)
	}
}

// IsNotFound reports whether err is a 404 from GitHub.
func IsNotFound(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Status == http.StatusNotFound
}

func apiError(resp *http.Response, op string) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	msg := strings.TrimSpace(string(raw))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Message != "" {
		msg = payload.Message
	}

	e := &Error{Status: resp.StatusCode, Message: msg, Op: op}
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		e.RateLimited = true
		if s, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			e.ResetAt = time.Unix(s, 0)
		}
	}
	return e
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
