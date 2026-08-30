package dockerapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ListImages returns images matching the given label selectors.
func (c *Client) ListImages(ctx context.Context, labels ...string) ([]Image, error) {
	q := url.Values{}
	if len(labels) > 0 {
		q.Set("filters", filterArgs(map[string][]string{"label": labels}))
	}
	var out []Image
	if err := c.getJSON(ctx, "/images/json", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ImageExists reports whether a tag is present locally.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := c.getJSON(ctx, "/images/"+url.PathEscape(ref)+"/json", nil, nil)
	if err == nil {
		return true, nil
	}
	if NotFound(err) {
		return false, nil
	}
	return false, err
}

// PullImage fetches an image, relaying progress to w.
func (c *Client) PullImage(ctx context.Context, ref string, w io.Writer) error {
	name, tag := splitRef(ref)
	q := url.Values{"fromImage": {name}, "tag": {tag}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/images/create", q), nil)
	if err != nil {
		return err
	}
	if auth := registryAuth(name); auth != "" {
		req.Header.Set("X-Registry-Auth", auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw)), Path: "/images/create"}
	}
	return relayBuildOutput(resp.Body, w)
}

// TagImage adds a new tag to an existing image.
func (c *Client) TagImage(ctx context.Context, source, target string) error {
	name, tag := splitRef(target)
	q := url.Values{"repo": {name}, "tag": {tag}}
	return c.postJSON(ctx, "/images/"+url.PathEscape(source)+"/tag", q, nil, nil)
}

// PushImage uploads an image to its registry, relaying progress to w.
func (c *Client) PushImage(ctx context.Context, ref string, w io.Writer) error {
	name, tag := splitRef(ref)
	q := url.Values{"tag": {tag}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/images/"+url.PathEscape(name)+"/push", q), nil)
	if err != nil {
		return err
	}
	// The daemon rejects a push without this header even for public repos.
	auth := registryAuth(name)
	if auth == "" {
		auth = base64.URLEncoding.EncodeToString([]byte("{}"))
	}
	req.Header.Set("X-Registry-Auth", auth)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		return &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw)), Path: "/images/push"}
	}
	return relayBuildOutput(resp.Body, w)
}

// RemoveImage deletes an image tag.
func (c *Client) RemoveImage(ctx context.Context, ref string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/images/"+url.PathEscape(ref), q, nil)
	if err != nil {
		if NotFound(err) || Conflict(err) {
			return nil
		}
		return err
	}
	resp.Body.Close()
	return nil
}

// splitRef separates an image reference into name and tag. A digest
// reference is returned whole with an empty tag.
func splitRef(ref string) (name, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref, ""
	}
	// Only treat the last colon as a tag separator if it is not part of a
	// registry host:port prefix.
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i+1:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// registryAuth reads ~/.docker/config.json and returns the X-Registry-Auth
// header value for the registry serving name, if credentials are stored.
func registryAuth(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(home + "/.docker/config.json")
	if err != nil {
		return ""
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}

	registry := "https://index.docker.io/v1/"
	if i := strings.Index(name, "/"); i > 0 && (strings.Contains(name[:i], ".") || strings.Contains(name[:i], ":")) {
		registry = name[:i]
	}

	entry, ok := cfg.Auths[registry]
	if !ok {
		// Fall back to a host match ignoring the scheme.
		for k, v := range cfg.Auths {
			if strings.TrimPrefix(strings.TrimPrefix(k, "https://"), "http://") == registry {
				entry, ok = v, true
				break
			}
		}
	}
	if !ok || entry.Auth == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
	if err != nil {
		return ""
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	if !found {
		return ""
	}
	cred, _ := json.Marshal(map[string]string{
		"username":      user,
		"password":      pass,
		"serveraddress": registry,
	})
	return base64.URLEncoding.EncodeToString(cred)
}
