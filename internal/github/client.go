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
	"sort"
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
//
// owner is the account whose repositories the request is about, or empty
// when the call is not repository-scoped. A personal access token ignores
// it; a GitHub App uses it to pick which of its installations to act as,
// since an App installed on three accounts holds three separate tokens and
// only one of them can see any given repository.
type authenticator interface {
	token(ctx context.Context, c *Client, owner string) (string, error)
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
		c.auth = &appAuth{appID: set.AppID, pinned: set.InstallationID, key: key}
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

func (a *tokenAuth) token(context.Context, *Client, string) (string, error) { return a.pat, nil }
func (a *tokenAuth) scheme() string                                         { return "Bearer" }

// appAuth authenticates as a GitHub App installation, minting and caching
// installation tokens as they expire.
//
// An App can be installed on several accounts at once, and each
// installation is a separate credential that only sees that account's
// repositories. So this holds a token per installation and picks between
// them by the account a request is about, rather than treating "the
// installation" as a single thing.
type appAuth struct {
	appID string
	// pinned is the installation ID an operator set by hand. Empty means
	// every installation the App has, which is what someone who installed
	// it on their personal account and two organisations expects.
	pinned string
	key    *rsa.PrivateKey

	mu       sync.Mutex
	installs []Installation
	tokens   map[string]*cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

func (a *appAuth) scheme() string { return "Bearer" }

func (a *appAuth) token(ctx context.Context, c *Client, owner string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	id, err := a.installationFor(ctx, c, owner)
	if err != nil {
		return "", err
	}
	// Refresh a minute early: a token that expires mid-request produces a
	// confusing 401 on an operation that had nothing wrong with it.
	if tok := a.tokens[id]; tok != nil && time.Now().Before(tok.expires.Add(-time.Minute)) {
		return tok.token, nil
	}

	jwt, err := a.appJWT()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/app/installations/%s/access_tokens", c.base, url.PathEscape(id)), nil)
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
	if a.tokens == nil {
		a.tokens = map[string]*cachedToken{}
	}
	a.tokens[id] = &cachedToken{token: out.Token, expires: out.ExpiresAt}
	return out.Token, nil
}

// installationFor picks which installation a request belongs to.
//
// The caller must hold a.mu.
func (a *appAuth) installationFor(ctx context.Context, c *Client, owner string) (string, error) {
	if a.pinned != "" {
		return a.pinned, nil
	}
	insts, err := a.list(ctx, c)
	if err != nil {
		return "", err
	}
	if owner != "" {
		for _, inst := range insts {
			if strings.EqualFold(inst.Account.Login, owner) {
				return strconv.FormatInt(inst.ID, 10), nil
			}
		}
		return "", fmt.Errorf("the GitHub App is not installed on %q; it is installed on %s", owner, accountList(insts))
	}
	if len(insts) == 1 {
		return strconv.FormatInt(insts[0].ID, 10), nil
	}
	// Every repository-scoped call carries an owner, so this is only
	// reachable for a call that is about no account in particular. There
	// is no right answer, and guessing would silently show one account's
	// world as if it were everything.
	return "", fmt.Errorf("this GitHub App is installed on %d accounts (%s); this request is not about any one of them",
		len(insts), accountList(insts))
}

func accountList(insts []Installation) string {
	names := make([]string, 0, len(insts))
	for _, i := range insts {
		names = append(names, fmt.Sprintf("%s (%d)", i.Account.Login, i.ID))
	}
	return strings.Join(names, ", ")
}

// list returns the App's installations, fetching them once. An App's
// installations change rarely, and re-listing them on every token refresh
// or settings page load spends a request for nothing.
//
// The caller must hold a.mu.
func (a *appAuth) list(ctx context.Context, c *Client) ([]Installation, error) {
	if len(a.installs) > 0 {
		return a.installs, nil
	}
	jwt, err := a.appJWT()
	if err != nil {
		return nil, err
	}
	// A pinned installation is the only one publix acts as, so listing the
	// others would describe access it does not use.
	if a.pinned != "" {
		one, err := fetchInstallation(ctx, c, jwt, a.pinned)
		if err != nil {
			return nil, err
		}
		a.installs = []Installation{*one}
		return a.installs, nil
	}
	found, err := listInstallations(ctx, c, jwt)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("this GitHub App has no installations yet — install it on the account or organisation whose repositories you want to deploy")
	}
	a.installs = found
	return a.installs, nil
}

// listInstallations reads every installation of the App. It needs the App
// JWT: an installation token cannot see the installations beside it.
func listInstallations(ctx context.Context, c *Client, jwt string) ([]Installation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/app/installations?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "publix")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, apiError(resp, "listing app installations")
	}
	var out []Installation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Account.Login) < strings.ToLower(out[j].Account.Login)
	})
	return out, nil
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

// Installation describes one installation of the App: which account it is
// on, and how much of that account it was given.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		Type      string `json:"type"`
	} `json:"account"`
	// RepositorySelection is "all" or "selected". The second is the usual
	// reason an operator connects an App successfully and then sees an
	// empty repository list.
	RepositorySelection string `json:"repository_selection"`
	// HTMLURL is the installation's own settings page, which is where the
	// repository selection is changed.
	HTMLURL string `json:"html_url"`
}

// Installations describes every installation publix can act as: which
// accounts the App is on, and how much of each it was given. The second
// result is false under a personal access token, where there is no
// installation to describe.
//
// This needs the App JWT: an installation token cannot read the object
// that granted it, nor see the installations beside it.
func (c *Client) Installations(ctx context.Context) ([]Installation, bool, error) {
	a, ok := c.auth.(*appAuth)
	if !ok {
		return nil, false, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	insts, err := a.list(ctx, c)
	if err != nil {
		return nil, true, err
	}
	return insts, true, nil
}

// CurrentInstallation describes the single installation publix acts as.
// It reports false when there is none to name: under a personal access
// token, or when the App spans several accounts and publix uses them all.
func (c *Client) CurrentInstallation(ctx context.Context) (*Installation, bool, error) {
	insts, isApp, err := c.Installations(ctx)
	if !isApp || err != nil {
		return nil, isApp, err
	}
	if len(insts) != 1 {
		return nil, false, nil
	}
	return &insts[0], true, nil
}

func fetchInstallation(ctx context.Context, c *Client, jwt, id string) (*Installation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/app/installations/%s", c.base, url.PathEscape(id)), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "publix")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, apiError(resp, "GET /app/installations/"+id)
	}
	var out Installation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs an authenticated API request, routed to whichever App
// installation owns the repository in the path.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (*http.Response, error) {
	return c.doAs(ctx, ownerFromPath(path), method, path, body, out)
}

// ownerFromPath reads the account out of a /repos/{owner}/{repo}/... path.
//
// Deriving it here rather than threading an owner argument through every
// call means a new repository-scoped method is routed correctly without
// having to remember to say so.
func ownerFromPath(path string) string {
	rest, ok := strings.CutPrefix(path, "/repos/")
	if !ok {
		return ""
	}
	owner, _, _ := strings.Cut(rest, "/")
	return owner
}

// doAs performs an authenticated API request as the installation covering
// owner. An empty owner means the call is not about one account.
func (c *Client) doAs(ctx context.Context, owner, method, path string, body, out any) (*http.Response, error) {
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

	tok, err := c.auth.token(ctx, c, owner)
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

// AppInfo describes the GitHub App itself, as opposed to an installation of
// it. Its hook attributes are what publix needs: an App delivers webhooks
// for every repository it is installed on, so when one is configured there
// is no reason to also create a webhook on each repository — and every
// reason not to, since GitHub would then deliver each push twice.
type AppInfo struct {
	ID      int64  `json:"id"`
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Owner   struct {
		Login string `json:"login"`
	} `json:"owner"`
	HookAttributes struct {
		URL    string `json:"url"`
		Active bool   `json:"active"`
	} `json:"hook_attributes"`
}

// App returns the App's own metadata. The second result is false when
// publix is authenticated with a personal access token rather than an App,
// in which case there is no App to describe.
//
// This is one of the few endpoints that needs the App JWT rather than an
// installation token: it describes the App, which no installation owns.
func (c *Client) App(ctx context.Context) (*AppInfo, bool, error) {
	a, ok := c.auth.(*appAuth)
	if !ok {
		return nil, false, nil
	}

	jwt, err := a.appJWT()
	if err != nil {
		return nil, true, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/app", nil)
	if err != nil {
		return nil, true, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "publix")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("calling GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, true, apiError(resp, "GET /app")
	}

	var info AppInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, true, err
	}
	return &info, true, nil
}
