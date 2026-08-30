package github

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	const secret = "correct horse battery staple"

	if err := VerifySignature(body, Sign(body, secret), secret); err != nil {
		t.Fatalf("a correctly signed body was rejected: %v", err)
	}

	// Every one of these must be refused: this endpoint is internet-facing
	// and accepting any of them would let a stranger trigger deploys.
	cases := map[string]struct{ header, secret string }{
		"wrong secret":  {Sign(body, secret), "wrong"},
		"empty header":  {"", secret},
		"sha1 format":   {"sha1=" + strings.Repeat("a", 40), secret},
		"not hex":       {"sha256=zzzz", secret},
		"truncated":     {Sign(body, secret)[:20], secret},
		"empty secret":  {Sign(body, secret), ""},
		"other payload": {Sign([]byte("{}"), secret), secret},
	}
	for name, tc := range cases {
		if err := VerifySignature(body, tc.header, tc.secret); err == nil {
			t.Errorf("%s: signature should have been rejected", name)
		}
	}
}

func TestParseWebhookRejectsUnsigned(t *testing.T) {
	req := httptest.NewRequest("POST", "/hook", strings.NewReader(`{}`))
	req.Header.Set("X-GitHub-Event", "push")
	if _, err := ParseWebhook(req, "secret"); err == nil {
		t.Fatal("an unsigned webhook must be rejected")
	}
}

func TestParseWebhookPush(t *testing.T) {
	body := `{
	  "ref": "refs/heads/main",
	  "after": "abc123def456",
	  "repository": {"name":"app","full_name":"acme/app","clone_url":"https://github.com/acme/app.git","owner":{"login":"acme"},"default_branch":"main"},
	  "head_commit": {"id":"abc123def456","message":"Fix the thing\n\nlonger body","author":{"name":"Ada"}},
	  "pusher": {"name":"ada"}
	}`
	req := httptest.NewRequest("POST", "/hook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", Sign([]byte(body), "s3cret"))

	d, err := ParseWebhook(req, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if d.Push == nil {
		t.Fatal("push payload was not decoded")
	}
	if got := d.Push.Branch(); got != "main" {
		t.Errorf("Branch() = %q, want main", got)
	}
	if got := d.Push.Owner(); got != "acme" {
		t.Errorf("Owner() = %q, want acme", got)
	}
	if got := d.Push.Message(); got != "Fix the thing" {
		t.Errorf("Message() = %q, want only the subject line", got)
	}
	if got := d.Push.Author(); got != "Ada" {
		t.Errorf("Author() = %q, want Ada", got)
	}
}

func TestPushEventTagIsNotABranch(t *testing.T) {
	e := &PushEvent{Ref: "refs/tags/v1.0.0"}
	if e.Branch() != "" {
		t.Errorf("a tag push should not report a branch, got %q", e.Branch())
	}
}

func TestSkipCIMarkers(t *testing.T) {
	for _, msg := range []string{"docs: tweak [skip ci]", "chore [ci skip]", "wip [no deploy]", "x [SKIP CI]"} {
		e := &PushEvent{}
		e.HeadCommit = &struct {
			ID      string `json:"id"`
			Message string `json:"message"`
			Author  struct {
				Name     string `json:"name"`
				Username string `json:"username"`
			} `json:"author"`
		}{Message: msg}
		if !e.SkipCI() {
			t.Errorf("%q should be treated as skip-ci", msg)
		}
	}
}
