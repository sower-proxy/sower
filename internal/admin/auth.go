package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// persistLocked writes the current sessions to disk atomically with 0600
// permissions. It must be called with s.mu held. IO errors degrade to an
// in-memory session store, never to a failed request.
func (s *Server) persistLocked() {
	if s.sessionFile == "" {
		return
	}
	data, err := json.Marshal(s.sessions)
	if err != nil {
		slog.Warn("marshal sessions", "error", err)
		return
	}
	dir := filepath.Dir(s.sessionFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("create session dir", "error", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*")
	if err != nil {
		slog.Warn("create session temp file", "error", err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		slog.Warn("write session temp file", "error", err)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		slog.Warn("chmod session temp file", "error", err)
		return
	}
	if err := tmp.Close(); err != nil {
		slog.Warn("close session temp file", "error", err)
		return
	}
	if err := os.Rename(tmpName, s.sessionFile); err != nil {
		slog.Warn("rename session file", "error", err)
		return
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !secureEqual(body.Password, s.opts.Password) {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	if !s.issueSession(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.persistLocked()
		s.mu.Unlock()
	}
	http.SetCookie(w, s.sessionCookie("", -1, r.TLS != nil))
	w.WriteHeader(http.StatusNoContent)
}

// sessionCookie builds the session cookie. The Secure flag is only set when
// the admin server runs over TLS; the default loopback HTTP listener would
// otherwise never receive the cookie.
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
		s.purgeExpiredLocked()
	}
	s.sessions[value] = time.Now().Add(sessionTTL)
	s.persistLocked()
	s.mu.Unlock()

	http.SetCookie(w, s.sessionCookie(value, int(sessionCookieMaxAge.Seconds()), r.TLS != nil))
	return true
}

func (s *Server) purgeExpiredLocked() {
	now := time.Now()
	purged := false
	for token, exp := range s.sessions {
		if now.After(exp) {
			delete(s.sessions, token)
			purged = true
		}
	}
	if purged {
		s.persistLocked()
	}
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return false
	}
	now := time.Now()
	if now.After(exp) {
		delete(s.sessions, c.Value)
		s.persistLocked()
		return false
	}
	// Sliding expiry: refresh a session that is more than halfway through
	// its TTL so an active user is never logged out mid-session, while an
	// inactive session still lapses. Persisting only on refresh bounds the
	// disk writes to a couple per session per TTL.
	if exp.Sub(now) < sessionTTL/2 {
		s.sessions[c.Value] = now.Add(sessionTTL)
		s.persistLocked()
	}
	return true
}

// auth rejects requests without a valid session cookie.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validSession(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
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
