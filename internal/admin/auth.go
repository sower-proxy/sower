package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// loadSessions restores persisted sessions at startup, dropping any that
// already expired while the process was down.
func (s *Server) loadSessions() {
	if s.sessionFile == "" {
		return
	}
	data, err := os.ReadFile(s.sessionFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("read session file", "error", err)
		}
		return
	}
	var stored map[string]time.Time
	if err := json.Unmarshal(data, &stored); err != nil {
		slog.Warn("parse session file", "error", err)
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, exp := range stored {
		if now.Before(exp) {
			s.sessions[token] = exp
		}
	}
	if len(s.sessions) > 0 {
		slog.Info("restored admin sessions", "count", len(s.sessions), "file", s.sessionFile)
	}
}

// persistLocked writes the supplied session snapshot to disk atomically with
// 0600 permissions. It must be called with s.mu held. IO errors are returned
// so callers can keep memory and durable state consistent.
func (s *Server) persistLocked(sessions map[string]time.Time) error {
	if s.sessionFile == "" {
		return nil
	}
	data, err := json.Marshal(sessions)
	if err != nil {
		return fmt.Errorf("marshal sessions for %s: %w", s.sessionFile, err)
	}
	dir := filepath.Dir(s.sessionFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("create session temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session temp file %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync session temp file %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close session temp file %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod session temp file %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.sessionFile); err != nil {
		return fmt.Errorf("rename session file %s: %w", s.sessionFile, err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func purgeExpired(sessions map[string]time.Time, now time.Time) {
	for token, exp := range sessions {
		if now.After(exp) {
			delete(sessions, token)
		}
	}
}

// remoteIP extracts the host part of a remote address for login throttling.
// X-Forwarded-For is not trusted: the admin listener is typically loopback,
// and a reverse-proxied deployment throttles per-proxy instead.
func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// Login throttling bounds password guessing when the admin listener is not
// loopback-only (the console can be bound to a LAN address or share port 80).
// Consecutive failures from one remote IP are rejected with 429 for a backoff
// window; a success clears the entry.
const (
	loginMaxFails     = 5
	loginLockDuration = 15 * time.Minute
)

type loginFail struct {
	count int
	until time.Time
}

type loginThrottle struct {
	mu    sync.Mutex
	fails map[string]loginFail
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{fails: make(map[string]loginFail)}
}

func (t *loginThrottle) allow(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.fails[ip]
	return !ok || time.Now().After(f.until)
}

func (t *loginThrottle) fail(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	f := t.fails[ip]
	f.count++
	if f.count >= loginMaxFails {
		f.until = now.Add(loginLockDuration)
		f.count = 0
	}
	t.fails[ip] = f
}

func (t *loginThrottle) success(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, ip)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r.RemoteAddr)
	if !s.throttle.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !secureEqual(body.Password, s.opts.Password) {
		s.throttle.fail(ip)
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	s.throttle.success(ip)
	if !s.issueSession(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		if err := s.persistLocked(s.sessions); err != nil {
			slog.Error("persist admin logout session", "error", err)
		}
		s.mu.Unlock()
	}

	http.SetCookie(w, s.sessionCookie("", -1, s.cookieSecure(r)))
	w.WriteHeader(http.StatusNoContent)
}

// sessionCookie builds the session cookie. The Secure flag is set when the
// listener uses TLS or when an explicitly configured TLS-terminating proxy
// protects the admin origin.
func (s *Server) sessionCookie(value string, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   maxAge,
	}
}

func (s *Server) cookieSecure(r *http.Request) bool {
	return r.TLS != nil || s.opts.CookieSecure
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request) bool {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		slog.Error("generate session token", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to issue session")
		return false
	}
	value := hex.EncodeToString(token[:])

	s.mu.Lock()
	if len(s.sessions) >= maxSessionPurge {
		purgeExpired(s.sessions, time.Now())
	}
	if len(s.sessions) >= maxSessionPurge {
		// Every retained session is still live; issuing another would grow
		// the map unboundedly. Refuse instead of evicting a valid session.
		s.mu.Unlock()
		slog.Warn("admin session limit reached, login rejected")
		writeError(w, http.StatusServiceUnavailable, "session limit reached, try later")
		return false
	}
	s.sessions[value] = time.Now().Add(sessionTTL)
	if err := s.persistLocked(s.sessions); err != nil {
		slog.Error("persist admin login session", "error", err)
	}
	s.mu.Unlock()

	http.SetCookie(w, s.sessionCookie(value, int(sessionCookieMaxAge.Seconds()), s.cookieSecure(r)))
	return true
}

// validSession returns the token, whether it is valid, and whether its
// server-side expiration was renewed. Persistence failures retain the prior
// expiry so a later request can retry the renewal.
func (s *Server) validSession(r *http.Request) (string, bool, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return "", false, false
	}
	now := time.Now()
	if now.After(exp) {
		delete(s.sessions, c.Value)
		if err := s.persistLocked(s.sessions); err != nil {
			slog.Warn("persist expired admin session cleanup", "error", err)
		}
		return "", false, false
	}
	if exp.Sub(now) < sessionTTL/2 {
		s.sessions[c.Value] = now.Add(sessionTTL)
		if err := s.persistLocked(s.sessions); err != nil {
			slog.Warn("renew admin session", "error", err)
		}
		return c.Value, true, true
	}
	return c.Value, true, false
}

// auth rejects requests without a valid session cookie and renews the
// browser's Max-Age for every authenticated HTTP response.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, valid, _ := s.validSession(r)
		if !valid {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		http.SetCookie(w, s.sessionCookie(token, int(sessionCookieMaxAge.Seconds()), s.cookieSecure(r)))
		next(w, r)
	}
}

// mutateGuard rejects cross-origin state-changing requests as CSRF defense.
func (s *Server) mutateGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
		}
		next(w, r)
	}
}

// secureEqual compares two secrets in constant time regardless of length.
func secureEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// GeneratePassword returns a cryptographically random 128-bit password
// (hex-encoded). cmd/sower uses it when the admin console is enabled
// without a configured password, so the listener never runs with an empty
// credential. The generated value is printed once in the startup log and
// changes on every restart.
func GeneratePassword() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("read crypto/rand: %v", err))
	}
	return hex.EncodeToString(b[:])
}
