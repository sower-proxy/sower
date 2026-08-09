package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/sower-proxy/sower/pkg/upstreamtls"
)

// Apply modes for config fields.
const (
	// ApplyImmediate takes effect at runtime without a restart.
	ApplyImmediate = "immediate"
	// ApplyRestart requires a process restart; shown read-only in v1.
	ApplyRestart = "restart"
	// ApplyReadonly can never be changed through the console.
	ApplyReadonly = "readonly"
)

// Config sources for display.
const (
	// SourceConfig marks values from the config file/flags (or defaults).
	// aconfig merges defaults before the file, so the two cannot be told
	// apart after load.
	SourceConfig = "config"
	// SourceOverride marks values overridden through the admin console and
	// persisted in the admin state file.
	SourceOverride = "override"
)

// ConfigField is one rendered config entry. Secret fields never carry a
// Value; Configured reports whether one is set. Type/Options let the console
// render a constrained choice (e.g. an enum select) instead of free text.
type ConfigField struct {
	Key        string   `json:"key"`
	Value      string   `json:"value,omitempty"`
	Editable   bool     `json:"editable"`
	ApplyMode  string   `json:"applyMode"`
	Source     string   `json:"source"`
	Constraint string   `json:"constraint,omitempty"`
	Secret     bool     `json:"secret,omitempty"`
	Configured bool     `json:"configured,omitempty"`
	Type       string   `json:"type,omitempty"`
	Options    []string `json:"options,omitempty"`
}

// ConfigSection groups related fields for display.
type ConfigSection struct {
	Name   string        `json:"name"`
	Fields []ConfigField `json:"fields"`
}

// ConfigView is the GET /api/config response. Revision drives optimistic
// concurrency for PATCHes.
type ConfigView struct {
	Revision uint64          `json:"revision"`
	Sections []ConfigSection `json:"sections"`
}

// ConfigChanges is the whitelisted PATCH payload. A nil pointer leaves the
// override unchanged; a non-nil empty string clears it, reverting the field
// to the file/flag configuration. List fields (rule sources) follow the
// same rule with slices.
type ConfigChanges struct {
	LogLevel    *string `json:"log_level"`
	DNSUpstream *string `json:"dns_upstream"`
	DNSFallback *string `json:"dns_fallback"`

	RemoteType                  *string `json:"remote_type"`
	RemoteAddr                  *string `json:"remote_addr"`
	RemoteTLSServerName         *string `json:"remote_tls_server_name"`
	RemoteTLSClientHello        *string `json:"remote_tls_client_hello"`
	RemoteTLSInsecureSkipVerify *string `json:"remote_tls_insecure_skip_verify"`

	DNSServe  *string `json:"dns_serve"`
	DNSServe6 *string `json:"dns_serve6"`

	Socks5Addr *string `json:"socks5_addr"`

	AdminSessionFile               *string `json:"admin_session_file"`
	AdminDisableSessionPersistence *string `json:"admin_disable_session_persistence"`
	AdminCookieSecure              *string `json:"admin_cookie_secure"`
	AdminStateFile                 *string `json:"admin_state_file"`

	RouterBlockFile           *string   `json:"router_block_file"`
	RouterBlockFilePrefix     *string   `json:"router_block_file_prefix"`
	RouterBlockFileSkipRules  *[]string `json:"router_block_file_skip_rules"`
	RouterBlockRules          *[]string `json:"router_block_rules"`
	RouterDirectFile          *string   `json:"router_direct_file"`
	RouterDirectFilePrefix    *string   `json:"router_direct_file_prefix"`
	RouterDirectFileSkipRules *[]string `json:"router_direct_file_skip_rules"`
	RouterDirectRules         *[]string `json:"router_direct_rules"`
	RouterProxyFile           *string   `json:"router_proxy_file"`
	RouterProxyFilePrefix     *string   `json:"router_proxy_file_prefix"`
	RouterProxyFileSkipRules  *[]string `json:"router_proxy_file_skip_rules"`
	RouterProxyRules          *[]string `json:"router_proxy_rules"`
	RouterCountryMMDB         *string   `json:"router_country_mmdb"`
	RouterCountryFile         *string   `json:"router_country_file"`
	RouterCountryRules        *[]string `json:"router_country_rules"`
}

// Validate checks all supplied values. Clearing an override (empty string
// or empty list) is always valid.
func (c ConfigChanges) Validate() error {
	if c.LogLevel != nil && *c.LogLevel != "" {
		var lv slog.Level
		if err := lv.UnmarshalText([]byte(strings.ToUpper(*c.LogLevel))); err != nil {
			return fmt.Errorf("invalid log_level %q", *c.LogLevel)
		}
	}
	if c.DNSUpstream != nil && *c.DNSUpstream != "" && net.ParseIP(*c.DNSUpstream) == nil {
		return fmt.Errorf("invalid dns_upstream %q", *c.DNSUpstream)
	}
	if c.DNSFallback != nil && *c.DNSFallback != "" && net.ParseIP(*c.DNSFallback) == nil {
		return fmt.Errorf("invalid dns_fallback %q", *c.DNSFallback)
	}
	if c.DNSServe != nil && *c.DNSServe != "" && net.ParseIP(*c.DNSServe) == nil {
		return fmt.Errorf("invalid dns_serve %q", *c.DNSServe)
	}
	if c.DNSServe6 != nil && *c.DNSServe6 != "" && net.ParseIP(*c.DNSServe6) == nil {
		return fmt.Errorf("invalid dns_serve6 %q", *c.DNSServe6)
	}
	if c.RemoteType != nil && *c.RemoteType != "" {
		switch *c.RemoteType {
		case "sower", "socks5":
		default:
			return fmt.Errorf("invalid remote_type %q", *c.RemoteType)
		}
	}
	if c.RemoteTLSClientHello != nil && *c.RemoteTLSClientHello != "" {
		if err := upstreamtls.ValidateClientHello(*c.RemoteTLSClientHello); err != nil {
			return fmt.Errorf("invalid remote_tls_client_hello %q", *c.RemoteTLSClientHello)
		}
	}
	if c.RemoteTLSInsecureSkipVerify != nil && *c.RemoteTLSInsecureSkipVerify != "" {
		if _, err := strconv.ParseBool(*c.RemoteTLSInsecureSkipVerify); err != nil {
			return fmt.Errorf("invalid remote_tls_insecure_skip_verify %q", *c.RemoteTLSInsecureSkipVerify)
		}
	}
	if c.Socks5Addr != nil && *c.Socks5Addr != "" {
		if _, _, err := net.SplitHostPort(*c.Socks5Addr); err != nil {
			return fmt.Errorf("invalid socks5_addr %q: %w", *c.Socks5Addr, err)
		}
	}
	if c.AdminDisableSessionPersistence != nil && *c.AdminDisableSessionPersistence != "" {
		if _, err := strconv.ParseBool(*c.AdminDisableSessionPersistence); err != nil {
			return fmt.Errorf("invalid admin_disable_session_persistence %q", *c.AdminDisableSessionPersistence)
		}
	}
	if c.AdminCookieSecure != nil && *c.AdminCookieSecure != "" {
		if _, err := strconv.ParseBool(*c.AdminCookieSecure); err != nil {
			return fmt.Errorf("invalid admin_cookie_secure %q", *c.AdminCookieSecure)
		}
	}
	return nil
}

// ConfigManager is the config display/edit surface the admin server needs.
// The concrete adapter lives in cmd/sower; tests inject a fake.
type ConfigManager interface {
	ConfigView() ConfigView
	// ApplyConfigChanges persists the overrides and applies them to the
	// runtime, guarded by the caller's revision. It returns the refreshed
	// view. ErrRevisionMismatch reports a stale revision.
	ApplyConfigChanges(changes ConfigChanges, revision uint64) (ConfigView, error)
}

// handleConfigGet serves the sanitized effective configuration with
// per-field metadata. Secrets are reported as configured flags, never values.
func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Config.ConfigView())
}

// handleConfigPatch applies whitelisted config changes with optimistic
// concurrency: the caller's revision must match the state revision.
func (s *Server) handleConfigPatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64        `json:"revision"`
		Changes  ConfigChanges `json:"changes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := req.Changes.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view, err := s.opts.Config.ApplyConfigChanges(req.Changes, req.Revision)
	if errors.Is(err, ErrRevisionMismatch) {
		writeError(w, http.StatusConflict, "config changed since load; refresh and retry")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleRestart triggers a process restart. The callback is fire-and-forget
// from the handler's perspective: it must return before the process exits so
// the 202 response is delivered first.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Restart == nil {
		writeError(w, http.StatusNotImplemented, "restart unavailable")
		return
	}
	if err := s.opts.Restart(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
}
