package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxPayload caps how much of a webhook body publix will read. GitHub's own
// limit is 25 MB; anything near that is not a push event publix cares about,
// and reading it unbounded would be a trivial memory exhaustion vector.
const MaxPayload = 8 << 20

// PushEvent is the part of GitHub's push payload publix acts on.
type PushEvent struct {
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Created bool   `json:"created"`
	Deleted bool   `json:"deleted"`
	Forced  bool   `json:"forced"`

	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		CloneURL string `json:"clone_url"`
		Owner    struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`

	HeadCommit *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"author"`
	} `json:"head_commit"`

	Pusher struct {
		Name string `json:"name"`
	} `json:"pusher"`
}

// Branch returns the pushed branch, or "" if the push was to a tag.
func (e *PushEvent) Branch() string {
	if strings.HasPrefix(e.Ref, "refs/heads/") {
		return strings.TrimPrefix(e.Ref, "refs/heads/")
	}
	return ""
}

// Owner returns the repository owner's login.
func (e *PushEvent) Owner() string {
	if e.Repository.Owner.Login != "" {
		return e.Repository.Owner.Login
	}
	return e.Repository.Owner.Name
}

// Message returns the head commit's subject line.
func (e *PushEvent) Message() string {
	if e.HeadCommit == nil {
		return ""
	}
	if i := strings.IndexByte(e.HeadCommit.Message, '\n'); i >= 0 {
		return e.HeadCommit.Message[:i]
	}
	return e.HeadCommit.Message
}

// Author returns who wrote the head commit.
func (e *PushEvent) Author() string {
	if e.HeadCommit != nil && e.HeadCommit.Author.Name != "" {
		return e.HeadCommit.Author.Name
	}
	return e.Pusher.Name
}

// SkipCI reports whether the commit message asks CI to stand down. Honouring
// the convention matters: a docs-only commit should not spend a build slot
// or restart a production container.
func (e *PushEvent) SkipCI() bool {
	if e.HeadCommit == nil {
		return false
	}
	msg := strings.ToLower(e.HeadCommit.Message)
	for _, marker := range []string{"[skip ci]", "[ci skip]", "[skip publix]", "[no deploy]"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// Delivery is a verified, parsed webhook.
type Delivery struct {
	Event string
	ID    string
	Push  *PushEvent
	Raw   []byte
}

// ParseWebhook verifies a webhook's signature and decodes its payload.
//
// The signature check is not optional. This endpoint has to be reachable
// from the public internet for GitHub to call it, so anything that reaches
// it without a valid signature is not from GitHub — and acting on it would
// let anyone on the internet trigger deploys.
func ParseWebhook(r *http.Request, secret string) (*Delivery, error) {
	if secret == "" {
		return nil, fmt.Errorf("no webhook secret is configured, so incoming webhooks cannot be verified")
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPayload+1))
	if err != nil {
		return nil, fmt.Errorf("reading the webhook body: %w", err)
	}
	if len(body) > MaxPayload {
		return nil, fmt.Errorf("webhook payload is larger than %d bytes", MaxPayload)
	}

	if err := VerifySignature(body, r.Header.Get("X-Hub-Signature-256"), secret); err != nil {
		return nil, err
	}

	d := &Delivery{
		Event: r.Header.Get("X-GitHub-Event"),
		ID:    r.Header.Get("X-GitHub-Delivery"),
		Raw:   body,
	}
	if d.Event == "push" {
		var push PushEvent
		if err := json.Unmarshal(body, &push); err != nil {
			return nil, fmt.Errorf("decoding the push payload: %w", err)
		}
		d.Push = &push
	}
	return d, nil
}

// VerifySignature checks GitHub's HMAC-SHA256 signature over the raw body.
func VerifySignature(body []byte, header, secret string) error {
	if header == "" {
		return fmt.Errorf("the webhook has no X-Hub-Signature-256 header")
	}
	prefix, sig, ok := strings.Cut(header, "=")
	if !ok || prefix != "sha256" {
		return fmt.Errorf("unsupported webhook signature format %q", header)
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return fmt.Errorf("the webhook signature is not valid hex")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	// hmac.Equal is constant time: comparing with == would leak the
	// expected signature one byte at a time through timing.
	if !hmac.Equal(mac.Sum(nil), want) {
		return fmt.Errorf("the webhook signature does not match — check that the secret in GitHub matches the one in publix")
	}
	return nil
}

// Sign produces the header value GitHub would send for a body. It exists so
// the signature path can be tested against its own verifier.
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
