package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Repo is a repository as the dashboard's import screen needs it.
type Repo struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Owner         string    `json:"owner"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	CloneURL      string    `json:"clone_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Language      string    `json:"language"`
	PushedAt      time.Time `json:"pushed_at"`
	Archived      bool      `json:"archived"`
	Permissions   struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
	} `json:"permissions"`
}

// rawRepo is GitHub's own shape, which nests the owner.
type rawRepo struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
	FullName      string    `json:"full_name"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	CloneURL      string    `json:"clone_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Language      string    `json:"language"`
	PushedAt      time.Time `json:"pushed_at"`
	Archived      bool      `json:"archived"`
	Permissions   struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
	} `json:"permissions"`
}

func (r rawRepo) convert() Repo {
	return Repo{
		ID: r.ID, Name: r.Name, FullName: r.FullName, Owner: r.Owner.Login,
		Private: r.Private, Description: r.Description, CloneURL: r.CloneURL,
		HTMLURL: r.HTMLURL, DefaultBranch: r.DefaultBranch, Language: r.Language,
		PushedAt: r.PushedAt, Archived: r.Archived, Permissions: r.Permissions,
	}
}

// Viewer is the authenticated account.
type Viewer struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	Type      string `json:"type"`
}

// Whoami identifies the credentials in use, which is what the settings page
// shows to confirm a connection actually works.
//
// An App installation has no user, so it reports the account the App is
// installed on. Naming that account is the whole point of the field here:
// installing an App on your personal account when the repositories live in
// an organisation is the most common way to end up connected and still see
// nothing.
func (c *Client) Whoami(ctx context.Context) (*Viewer, error) {
	if inst, isApp, err := c.CurrentInstallation(ctx); isApp {
		if err != nil {
			return nil, err
		}
		return &Viewer{
			Login:     inst.Account.Login,
			Name:      inst.Account.Login,
			AvatarURL: inst.Account.AvatarURL,
			Type:      "Installation",
		}, nil
	}
	var v Viewer
	if _, err := c.do(ctx, http.MethodGet, "/user", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// ListRepos returns every repository the credentials can deploy from.
//
// The two credential styles read from different endpoints — a token sees
// the user's repositories, an App sees the ones its installation was
// granted — so both are collected and presented identically.
func (c *Client) ListRepos(ctx context.Context) ([]Repo, error) {
	if _, ok := c.auth.(*appAuth); ok {
		return c.listInstallationRepos(ctx)
	}
	return c.listUserRepos(ctx)
}

func (c *Client) listUserRepos(ctx context.Context) ([]Repo, error) {
	var out []Repo
	// 10 pages of 100 is 1000 repositories, which is far past the point
	// where a user is scrolling rather than searching.
	for page := 1; page <= 10; page++ {
		var batch []rawRepo
		path := fmt.Sprintf("/user/repos?per_page=100&page=%d&sort=pushed&affiliation=owner,collaborator,organization_member", page)
		if _, err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			out = append(out, r.convert())
		}
		if len(batch) < 100 {
			break
		}
	}
	return sortRepos(out), nil
}

func (c *Client) listInstallationRepos(ctx context.Context) ([]Repo, error) {
	var out []Repo
	for page := 1; page <= 10; page++ {
		var batch struct {
			Repositories []rawRepo `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page)
		if _, err := c.do(ctx, http.MethodGet, path, nil, &batch); err != nil {
			return nil, err
		}
		for _, r := range batch.Repositories {
			out = append(out, r.convert())
		}
		if len(batch.Repositories) < 100 {
			break
		}
	}
	return sortRepos(out), nil
}

// sortRepos puts the most recently pushed first, which is nearly always the
// one someone is looking for on an import screen.
func sortRepos(in []Repo) []Repo {
	sort.SliceStable(in, func(i, j int) bool { return in[i].PushedAt.After(in[j].PushedAt) })
	return in
}

// GetRepo fetches one repository.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (*Repo, error) {
	var r rawRepo
	path := fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(name))
	if _, err := c.do(ctx, http.MethodGet, path, nil, &r); err != nil {
		return nil, err
	}
	out := r.convert()
	return &out, nil
}

// Branch is one branch of a repository.
type Branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListBranches returns a repository's branches.
func (c *Client) ListBranches(ctx context.Context, owner, name string) ([]Branch, error) {
	var out []Branch
	path := fmt.Sprintf("/repos/%s/%s/branches?per_page=100", url.PathEscape(owner), url.PathEscape(name))
	if _, err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetFile reads one file from a repository at a ref.
func (c *Client) GetFile(ctx context.Context, owner, name, path, ref string) ([]byte, error) {
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
		Type     string `json:"type"`
	}
	u := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(name), path)
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	if _, err := c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, err
	}
	if out.Type != "file" {
		return nil, fmt.Errorf("%s is a %s, not a file", path, out.Type)
	}
	if out.Encoding != "base64" {
		return []byte(out.Content), nil
	}
	// GitHub wraps base64 content at 60 columns.
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
}

// TreeEntry is one path in a repository listing.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// ListRoot returns the repository's top-level entries. The import screen
// uses it to tell, without cloning, whether a repo has a Dockerfile, a
// compose file or a deployment.yaml.
func (c *Client) ListRoot(ctx context.Context, owner, name, ref string) ([]TreeEntry, error) {
	var out []TreeEntry
	u := fmt.Sprintf("/repos/%s/%s/contents/", url.PathEscape(owner), url.PathEscape(name))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	if _, err := c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PutFile creates or updates a file, which is how the dashboard offers to
// commit a generated deployment.yaml back to the repository.
func (c *Client) PutFile(ctx context.Context, owner, name, path, branch, message string, content []byte) error {
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if branch != "" {
		body["branch"] = branch
	}
	// Updating an existing file requires its blob SHA; creating one must
	// not send it at all.
	var existing struct {
		SHA string `json:"sha"`
	}
	u := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(name), path)
	getURL := u
	if branch != "" {
		getURL += "?ref=" + url.QueryEscape(branch)
	}
	if _, err := c.do(ctx, http.MethodGet, getURL, nil, &existing); err == nil && existing.SHA != "" {
		body["sha"] = existing.SHA
	}
	_, err := c.do(ctx, http.MethodPut, u, body, nil)
	return err
}

// Hook is a repository webhook.
type Hook struct {
	ID     int64    `json:"id"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// EnsureHook creates the push webhook publix needs, reusing an existing one
// pointing at the same URL so repeated imports do not pile up duplicates.
func (c *Client) EnsureHook(ctx context.Context, owner, name, callbackURL, secret string) (int64, error) {
	var existing []Hook
	list := fmt.Sprintf("/repos/%s/%s/hooks?per_page=100", url.PathEscape(owner), url.PathEscape(name))
	if _, err := c.do(ctx, http.MethodGet, list, nil, &existing); err != nil {
		return 0, err
	}
	for _, h := range existing {
		if h.Config.URL == callbackURL {
			// The secret may have been rotated since; update in place.
			body := map[string]any{
				"active": true,
				"events": []string{"push"},
				"config": map[string]any{
					"url": callbackURL, "content_type": "json", "secret": secret, "insecure_ssl": "0",
				},
			}
			patch := fmt.Sprintf("/repos/%s/%s/hooks/%d", url.PathEscape(owner), url.PathEscape(name), h.ID)
			if _, err := c.do(ctx, http.MethodPatch, patch, body, nil); err != nil {
				return h.ID, err
			}
			return h.ID, nil
		}
	}

	body := map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"push"},
		"config": map[string]any{
			"url": callbackURL, "content_type": "json", "secret": secret, "insecure_ssl": "0",
		},
	}
	var created Hook
	create := fmt.Sprintf("/repos/%s/%s/hooks", url.PathEscape(owner), url.PathEscape(name))
	if _, err := c.do(ctx, http.MethodPost, create, body, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// DeleteHook removes a webhook, so deleting a project does not leave a
// broken hook firing at a dead endpoint forever.
func (c *Client) DeleteHook(ctx context.Context, owner, name string, id int64) error {
	path := fmt.Sprintf("/repos/%s/%s/hooks/%d", url.PathEscape(owner), url.PathEscape(name), id)
	_, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// CommitStatus reports a deployment's outcome next to the commit on GitHub.
type CommitStatus struct {
	State       string `json:"state"` // pending, success, failure, error
	TargetURL   string `json:"target_url,omitempty"`
	Description string `json:"description,omitempty"`
	Context     string `json:"context"`
}

// SetCommitStatus posts a status to a commit.
func (c *Client) SetCommitStatus(ctx context.Context, owner, name, sha string, st CommitStatus) error {
	if sha == "" {
		return nil
	}
	if st.Context == "" {
		st.Context = "publix"
	}
	// GitHub truncates at 140 characters; do it ourselves so the message
	// ends in something readable rather than mid-word.
	if len(st.Description) > 140 {
		st.Description = st.Description[:137] + "..."
	}
	path := fmt.Sprintf("/repos/%s/%s/statuses/%s", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(sha))
	_, err := c.do(ctx, http.MethodPost, path, st, nil)
	return err
}

// Commit is a single commit's metadata.
type Commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

// GetCommit fetches one commit, used to fill in a deployment's metadata
// when a rollback targets a commit no longer in the shallow checkout.
func (c *Client) GetCommit(ctx context.Context, owner, name, ref string) (*Commit, error) {
	var out Commit
	path := fmt.Sprintf("/repos/%s/%s/commits/%s", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(ref))
	if _, err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AuthenticateCloneURL embeds credentials into an HTTPS clone URL so git can
// fetch a private repository without an interactive prompt.
func (c *Client) AuthenticateCloneURL(ctx context.Context, cloneURL string) (string, error) {
	tok, err := c.auth.token(ctx, c)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(cloneURL)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" {
		return cloneURL, nil
	}
	// "x-access-token" is the username GitHub expects for both App
	// installation tokens and personal access tokens.
	u.User = url.UserPassword("x-access-token", tok)
	return u.String(), nil
}
