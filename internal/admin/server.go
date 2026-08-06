package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sower-proxy/sower/web"
)

const (
	sessionCookieName = "sower_admin"
	sessionTTL        = 24 * time.Hour
	maxSessionPurge   = 1024

	maxBodyBytes     = 64 << 10
	maxRulesPerBatch = 100
	maxRuleLength    = 253

	defaultPageSize = 200
	maxPageSize     = 1000
)

// Category identifies a routing rule category managed through the admin API.
type Category string

const (
	CategoryBlock  Category = "block"
	CategoryDirect Category = "direct"
	CategoryProxy  Category = "proxy"
)

func (c Category) valid() bool {
	switch c {
	case CategoryBlock, CategoryDirect, CategoryProxy:
		return true
	default:
		return false
	}
}

// RuleManager is the rule operations surface the admin server needs. The
// concrete adapter lives in cmd/sower; tests inject a fake.
type RuleManager interface {
	RuleList(category Category) ([]string, error)
	RuleSearch(category Category, q string, offset, limit int) ([]string, uint64, error)
	RuleAdd(category Category, rules ...string) error
	RuleRemove(category Category, rule string) (bool, error)
	RuleCount(category Category) uint64
}

// Options configures the admin server.
type Options struct {
	Password    string
	Version     string
	Date        string
	Rules       RuleManager
	Stats       *Stats
	SessionFile string
}

// Server serves the admin API and the embedded frontend on one listener.
type Server struct {
	opts        Options
	sessions    map[string]time.Time
	sessionFile string
	mu          sync.Mutex
	http        *http.Server
}

func NewServer(opts Options) *Server {
	s := &Server{
		opts:        opts,
		sessions:    make(map[string]time.Time),
		sessionFile: opts.SessionFile,
	}
	s.loadSessions()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", s.handleLogin)
	mux.HandleFunc("DELETE /api/session", s.handleLogout)
	mux.HandleFunc("GET /api/status", s.mutateGuard(s.auth(s.handleStatus)))
	mux.HandleFunc("GET /api/rules", s.mutateGuard(s.auth(s.handleRulesList)))
	mux.HandleFunc("POST /api/rules", s.mutateGuard(s.auth(s.handleRulesAdd)))
	mux.HandleFunc("DELETE /api/rules", s.mutateGuard(s.auth(s.handleRulesRemove)))
	mux.HandleFunc("GET /api/traffic", s.mutateGuard(s.auth(s.handleTraffic)))
	mux.HandleFunc("GET /api/history", s.mutateGuard(s.auth(s.handleHistory)))
	if s.opts.Stats != nil {
		mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	mux.HandleFunc("/", s.handleStatic)

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

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

// Serve accepts connections on ln until Shutdown or a fatal serve error.
func (s *Server) Serve(ln net.Listener) error {
	err := s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// --- auth ---

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

	http.SetCookie(w, s.sessionCookie(value, int(sessionTTL.Seconds()), r.TLS != nil))
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

// --- API handlers ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.opts.Version,
		"date":    s.opts.Date,
		"uptime":  int64(time.Since(statStart(s.opts.Stats)).Seconds()),
		"rules": map[string]uint64{
			string(CategoryBlock):  s.opts.Rules.RuleCount(CategoryBlock),
			string(CategoryDirect): s.opts.Rules.RuleCount(CategoryDirect),
			string(CategoryProxy):  s.opts.Rules.RuleCount(CategoryProxy),
		},
	})
}

type rulesRequest struct {
	Category Category `json:"category"`
	Rules    []string `json:"rules"`
}

func (s *Server) handleRulesList(w http.ResponseWriter, r *http.Request) {
	category := Category(r.URL.Query().Get("category"))
	if !category.valid() {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	q := r.URL.Query().Get("q")
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	rules, total, err := s.opts.Rules.RuleSearch(category, q, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rulesResponse{
		Category: category,
		Rules:    rules,
		Total:    total,
		Offset:   offset,
		Limit:    limit,
	})
}

func (s *Server) handleRulesAdd(w http.ResponseWriter, r *http.Request) {
	var req rulesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	validated, err := validateRules(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Rules = validated
	if err := s.opts.Rules.RuleAdd(req.Category, req.Rules...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRulesRemove(w http.ResponseWriter, r *http.Request) {
	var req rulesRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	validated, err := validateRules(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Rules = validated
	for _, rule := range req.Rules {
		if _, err := s.opts.Rules.RuleRemove(req.Category, rule); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	sort := DomainSort(r.URL.Query().Get("sort"))
	if !sort.valid() {
		sort = DomainSortBytes
	}
	source := Source(r.URL.Query().Get("source"))
	if !source.valid() {
		source = SourceAll
	}
	writeJSON(w, http.StatusOK, s.opts.Stats.Snapshot(sort, source))
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.opts.Stats.History())
}

// handleMetrics serves the Prometheus exposition format. It is intentionally
// unauthenticated: scrapers cannot carry the session cookie, and the exposed
// metrics are aggregate counters without per-domain or credential data.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.opts.Stats == nil {
		writeError(w, http.StatusInternalServerError, "stats unavailable")
		return
	}
	s.opts.Stats.Metrics().ServeHTTP(w, r)
}

// --- static frontend ---

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// Unknown API paths must not fall through to the SPA shell.
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
		return
	}

	fsys, err := web.Dist()
	if err != nil {
		slog.Error("load embedded web dist", "error", err)
		writeError(w, http.StatusInternalServerError, "embedded web assets unavailable")
		return
	}

	// The embedded FS is read-only and r.URL.Path is already normalized by
	// net/http, so traversal cannot escape the dist directory.
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if !fs.ValidPath(p) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

	if _, err := fs.Stat(fsys, p); err != nil {
		p = "index.html" // SPA fallback for client-side routes
	}
	http.ServeFileFS(w, r, fsys, p)
}

// --- helpers ---

func statStart(stats *Stats) time.Time {
	if stats == nil {
		return time.Now()
	}
	return stats.start
}

type rulesResponse struct {
	Category Category `json:"category"`
	Rules    []string `json:"rules"`
	Total    uint64   `json:"total"`
	Offset   int      `json:"offset"`
	Limit    int      `json:"limit"`
}

func writeRulesResult(w http.ResponseWriter, rm RuleManager, category Category) {
	rules, err := rm.RuleList(category)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rulesResponse{Category: category, Rules: rules})
}

func validateRules(req rulesRequest) ([]string, error) {
	if !req.Category.valid() {
		return nil, errors.New("invalid category")
	}
	if len(req.Rules) == 0 {
		return nil, errors.New("no rules provided")
	}
	if len(req.Rules) > maxRulesPerBatch {
		return nil, fmt.Errorf("too many rules, max %d", maxRulesPerBatch)
	}

	out := make([]string, 0, len(req.Rules))
	for _, rule := range req.Rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			return nil, errors.New("empty rule")
		}
		if len(rule) > maxRuleLength {
			return nil, fmt.Errorf("rule too long, max %d bytes", maxRuleLength)
		}
		if strings.ContainsAny(rule, "\r\n") {
			return nil, errors.New("rule must not contain line breaks")
		}
		out = append(out, rule)
	}
	return out, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "unexpected trailing JSON data")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// secureEqual compares two secrets in constant time regardless of length.
func secureEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
