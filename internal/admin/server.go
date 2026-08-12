package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sower-proxy/sower/web"
)

const (
	sessionCookieName = "sower_admin"
	sessionTTL        = 24 * time.Hour
	// sessionCookieMaxAge is how long the browser keeps the session cookie.
	// It is deliberately much longer than sessionTTL: the cookie is only a
	// bearer-token carrier, and the server-side session map (with sliding
	// expiry) is the real gate. A short MaxAge would log the user out after
	// 24h even while the server session stays refreshed.
	sessionCookieMaxAge = 30 * 24 * time.Hour
	maxSessionPurge     = 1024

	maxBodyBytes     = 64 << 10
	maxRulesPerBatch = 100
	maxRuleLength    = 253

	defaultPageSize = 200
	maxPageSize     = 1000
	// trafficCacheTTL equals the SSE traffic tick period, so the shared
	// default-view cache still lets one client per window advance the history
	// ring at the same cadence as a single uncached stream.
	trafficCacheTTL = 5 * time.Second
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
	// RuleSearch returns a page of retained rules for the category, filtered
	// by q and ordered by sortBy in direction dir (see RuleSort constants).
	RuleSearch(category Category, q string, offset, limit int, sortBy RuleSort, dir SortDir) ([]RuleEntry, uint64, error)
	RuleAdd(category Category, rules ...string) error
	RuleRemove(category Category, rule string) (bool, error)
	// RuleRemoveMany removes a batch atomically from the persisted state and
	// returns only the effective runtime removals.
	RuleRemoveMany(category Category, rules ...string) ([]string, error)
	RuleCount(category Category) uint64
	// RuleChanges returns the persisted rule deltas relative to the boot
	// baseline.
	RuleChanges() RuleChangeSet
	// RuleReset clears the deltas of one category, or of all categories when
	// category is empty, rebuilding the runtime rule sets to the baseline.
	RuleReset(category Category) error
	TestDomain(domain string) (DomainTest, error)
}

// Options configures the admin server.
type Options struct {
	Password          string
	TemporaryPassword bool
	Version           string
	Date              string
	Rules             RuleManager
	Stats             *Stats
	SessionFile       string
	CookieSecure      bool
	// Config enables the config display/edit endpoints when non-nil.
	Config ConfigManager
	// Restart triggers a process restart when non-nil; the endpoint returns
	// 501 when nil. The callback must return before the process exits so the
	// HTTP response can be delivered.
	Restart func() error
	// Hostnames resolves client IPs to hostnames for the traffic console;
	// reverse lookups are skipped when nil.
	Hostnames HostnameResolver
}

// Server serves the admin API and the embedded frontend on one listener.
type Server struct {
	opts        Options
	sessions    map[string]time.Time
	sessionFile string
	mu          sync.Mutex
	trafficMu   sync.Mutex
	traffic     cachedTraffic
	hostnames   *hostnameCache
	throttle    *loginThrottle
	http        *http.Server
}

type cachedTraffic struct {
	at       time.Time
	snapshot TrafficSnapshot
}

func NewServer(opts Options) *Server {
	s := &Server{
		opts:        opts,
		sessions:    make(map[string]time.Time),
		sessionFile: opts.SessionFile,
		throttle:    newLoginThrottle(),
	}
	if opts.Hostnames != nil {
		s.hostnames = newHostnameCache(opts.Hostnames)
	}
	s.loadSessions()

	mux := http.NewServeMux()
	// The login endpoint carries the same Origin guard as the other
	// mutating endpoints so a cross-site page cannot drive password
	// guessing through the victim's browser.
	mux.HandleFunc("POST /api/session", s.mutateGuard(s.handleLogin))
	mux.HandleFunc("GET /api/session", s.mutateGuard(s.auth(s.handleSession)))
	mux.HandleFunc("DELETE /api/session", s.handleLogout)
	mux.HandleFunc("GET /api/login-info", s.handleLoginInfo)
	mux.HandleFunc("GET /api/status", s.mutateGuard(s.auth(s.handleStatus)))
	mux.HandleFunc("GET /api/rules", s.mutateGuard(s.auth(s.handleRulesList)))
	mux.HandleFunc("POST /api/rules", s.mutateGuard(s.auth(s.handleRulesAdd)))
	mux.HandleFunc("DELETE /api/rules", s.mutateGuard(s.auth(s.handleRulesRemove)))
	mux.HandleFunc("GET /api/rules/changes", s.mutateGuard(s.auth(s.handleRulesChanges)))
	mux.HandleFunc("POST /api/rules/reset", s.mutateGuard(s.auth(s.handleRulesReset)))
	mux.HandleFunc("GET /api/rules/test", s.mutateGuard(s.auth(s.handleRulesTest)))
	mux.HandleFunc("GET /api/rules/miss", s.mutateGuard(s.auth(s.handleRuleMiss)))
	mux.HandleFunc("GET /api/traffic", s.mutateGuard(s.auth(s.handleTraffic)))
	mux.HandleFunc("GET /api/totals", s.mutateGuard(s.auth(s.handleTotals)))
	mux.HandleFunc("GET /api/history", s.mutateGuard(s.auth(s.handleHistory)))
	mux.HandleFunc("GET /api/stream", s.mutateGuard(s.auth(s.handleStream)))
	if s.opts.Config != nil {
		mux.HandleFunc("GET /api/config", s.mutateGuard(s.auth(s.handleConfigGet)))
		mux.HandleFunc("PATCH /api/config", s.mutateGuard(s.auth(s.handleConfigPatch)))
	}
	if s.opts.Restart != nil {
		mux.HandleFunc("POST /api/restart", s.mutateGuard(s.auth(s.handleRestart)))
	}
	if s.opts.Stats != nil {
		mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	mux.HandleFunc("/", s.handleStatic)

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		// IdleTimeout bounds keep-alive connections between requests; the
		// SSE stream is an active response and is unaffected. MaxHeaderBytes
		// bounds the request head so a hostile client cannot grow memory
		// with oversized headers.
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	return s
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

// handleLoginInfo reports whether the admin console runs with a
// startup-generated temporary password, so the login page can tell the user
// where to find it. It is intentionally public: the login page needs it
// before authenticating, and it leaks no credential material.
func (s *Server) handleLoginInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"temporaryPassword": s.opts.TemporaryPassword,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusPayload())
}

// statusPayload is the /api/status body, reused by the SSE stream.
func (s *Server) statusPayload() map[string]any {
	return map[string]any{
		"version": s.opts.Version,
		"date":    s.opts.Date,
		"uptime":  int64(time.Since(statStart(s.opts.Stats)).Seconds()),
		"rules": map[string]uint64{
			string(CategoryBlock):  s.opts.Rules.RuleCount(CategoryBlock),
			string(CategoryDirect): s.opts.Rules.RuleCount(CategoryDirect),
			string(CategoryProxy):  s.opts.Rules.RuleCount(CategoryProxy),
		},
	}
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
	Category Category    `json:"category"`
	Rules    []RuleEntry `json:"rules"`
	Total    uint64      `json:"total"`
	Offset   int         `json:"offset"`
	Limit    int         `json:"limit"`
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
