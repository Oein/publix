package api

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, salt := HashPassword("correct horse battery staple")
	if !CheckPassword("correct horse battery staple", hash, salt) {
		t.Fatal("the correct password was rejected")
	}
	if CheckPassword("wrong", hash, salt) {
		t.Error("a wrong password was accepted")
	}
	// A different install must not produce the same verifier for the same
	// password, or one leaked hash would identify reuse across servers.
	other, otherSalt := HashPassword("correct horse battery staple")
	if hash == other || salt == otherSalt {
		t.Error("hashing is not salted per install")
	}
}

func TestCheckPasswordRejectsMalformedStoredValues(t *testing.T) {
	// A corrupt store must fail closed, never open.
	for _, tc := range []struct{ hash, salt string }{
		{"", ""},
		{"notahexstring", "abcd"},
		{"abcd", "notahexstring"},
	} {
		if CheckPassword("anything", tc.hash, tc.salt) {
			t.Errorf("malformed hash/salt %q/%q accepted a password", tc.hash, tc.salt)
		}
	}
}

func TestSessionTokenSigning(t *testing.T) {
	const key = "signing-key"
	token := issueSession(key, time.Now().Add(time.Hour))
	if !validSession(key, token) {
		t.Fatal("a freshly issued session was rejected")
	}

	if validSession("different-key", token) {
		t.Error("a session validated under the wrong key — rotating the key must revoke sessions")
	}
	if validSession(key, issueSession(key, time.Now().Add(-time.Minute))) {
		t.Error("an expired session was accepted")
	}

	// Tampering with the expiry must invalidate the signature, or anyone
	// could extend their own session indefinitely.
	payload, sig, _ := strings.Cut(token, ".")
	_ = payload
	forged := "99999999999." + sig
	if validSession(key, forged) {
		t.Error("a session with a tampered expiry was accepted")
	}
	for _, bad := range []string{"", "nodot", "abc.def", token + "x"} {
		if validSession(key, bad) {
			t.Errorf("malformed token %q was accepted", bad)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("attempt %d should have been allowed", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Error("the fourth attempt should have been refused")
	}
	// A different caller is unaffected.
	if !l.allow("5.6.7.8") {
		t.Error("a different address should not be rate limited")
	}
	// A successful login clears the counter.
	l.reset("1.2.3.4")
	if !l.allow("1.2.3.4") {
		t.Error("reset did not clear the counter")
	}
}
