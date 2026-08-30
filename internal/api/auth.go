package api

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/store"
)

// Session cookie name and lifetime.
const (
	sessionCookie = "publix_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// pbkdf2 cost. 600,000 iterations of SHA-256 is OWASP's 2023 guidance and
// costs roughly a quarter-second here — unnoticeable on a login, expensive
// enough to matter to someone working through a stolen state file.
const (
	pbkdfIterations = 600_000
	pbkdfKeyLen     = 32
)

// HashPassword derives a verifier for a new password.
func HashPassword(password string) (hash, salt string) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		panic("publix: system randomness unavailable: " + err.Error())
	}
	key, err := pbkdf2.Key(sha256.New, password, saltBytes, pbkdfIterations, pbkdfKeyLen)
	if err != nil {
		panic("publix: password hashing failed: " + err.Error())
	}
	return hex.EncodeToString(key), hex.EncodeToString(saltBytes)
}

// CheckPassword verifies a password against a stored hash and salt.
func CheckPassword(password, hash, salt string) bool {
	saltBytes, err := hex.DecodeString(salt)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(hash)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, saltBytes, pbkdfIterations, pbkdfKeyLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// issueSession mints a signed session token.
//
// The token is self-contained — "<expiry>.<hmac>" — so publix keeps no
// server-side session table and a restart does not log everyone out. The
// signing key lives in the store, so rotating it revokes every session.
func issueSession(key string, expires time.Time) string {
	payload := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// validSession reports whether a token is authentic and unexpired.
func validSession(key, token string) bool {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return false
	}
	unix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Before(time.Unix(unix, 0))
}

// setSessionCookie writes the session cookie.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		// The dashboard is normally served over TLS through Traefik, but a
		// first-run setup on plain HTTP must still be able to log in.
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// authenticated reports whether a request carries a valid session, or the
// API token in an Authorization header for scripted use.
func (s *Server) authenticated(r *http.Request) bool {
	set := s.store.Settings()
	if set.Auth.PasswordHash == "" {
		// Before setup, only the setup endpoints are reachable, and they
		// enforce that themselves.
		return false
	}
	if c, err := r.Cookie(sessionCookie); err == nil && validSession(set.Auth.SessionKey, c.Value) {
		return true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		token := strings.TrimSpace(strings.TrimPrefix(h, "Bearer"))
		if token != "" && validSession(set.Auth.SessionKey, token) {
			return true
		}
	}
	return false
}

// requireAuth wraps a handler so unauthenticated requests are refused.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("sign in to continue"))
			return
		}
		next(w, r)
	}
}

// handleAuthState tells the dashboard whether to show setup, login, or the
// application.
func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	set := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"needsSetup":    set.Auth.PasswordHash == "",
		"authenticated": s.authenticated(r),
	})
}

// handleSetup accepts the first password. It is only usable while no
// password exists, so it cannot be used to take over a configured install.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Password) < 8 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("choose a password of at least 8 characters"))
		return
	}

	var token string
	expires := time.Now().Add(sessionTTL)
	err := s.store.SetSettings(func(set *store.Settings) error {
		if set.Auth.PasswordHash != "" {
			return fmt.Errorf("this server is already set up")
		}
		set.Auth.PasswordHash, set.Auth.Salt = HashPassword(body.Password)
		if set.Auth.SessionKey == "" {
			set.Auth.SessionKey = store.NewToken()
		}
		token = issueSession(set.Auth.SessionKey, expires)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogin exchanges the password for a session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	set := s.store.Settings()
	if set.Auth.PasswordHash == "" {
		writeError(w, http.StatusConflict, fmt.Errorf("this server has not been set up yet"))
		return
	}

	if !s.loginLimiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, fmt.Errorf("too many attempts; wait a minute and try again"))
		return
	}
	if !CheckPassword(body.Password, set.Auth.PasswordHash, set.Auth.Salt) {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("that password is not correct"))
		return
	}

	s.loginLimiter.reset(clientIP(r))
	expires := time.Now().Add(sessionTTL)
	s.setSessionCookie(w, r, issueSession(set.Auth.SessionKey, expires), expires)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleLogout clears the session cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChangePassword rotates the password and invalidates every session,
// including the caller's, which is the point of rotating it.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(body.New) < 8 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("choose a password of at least 8 characters"))
		return
	}
	err := s.store.SetSettings(func(set *store.Settings) error {
		if !CheckPassword(body.Current, set.Auth.PasswordHash, set.Auth.Salt) {
			return fmt.Errorf("the current password is not correct")
		}
		set.Auth.PasswordHash, set.Auth.Salt = HashPassword(body.New)
		set.Auth.SessionKey = store.NewToken()
		return nil
	})
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	s.handleLogout(w, r)
}

// clientIP identifies the caller for rate limiting, trusting a proxy header
// only when publix is configured to sit behind one.
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		if i := strings.IndexByte(f, ','); i > 0 {
			return strings.TrimSpace(f[:i])
		}
		return strings.TrimSpace(f)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
