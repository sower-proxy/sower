package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
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
// Value; Configured reports whether one is set.
type ConfigField struct {
	Key        string `json:"key"`
	Value      string `json:"value,omitempty"`
	Editable   bool   `json:"editable"`
	ApplyMode  string `json:"applyMode"`
	Source     string `json:"source"`
	Constraint string `json:"constraint,omitempty"`
	Secret     bool   `json:"secret,omitempty"`
	Configured bool   `json:"configured,omitempty"`
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
// to the file/flag configuration.
type ConfigChanges struct {
	LogLevel    *string `json:"log_level"`
	DNSUpstream *string `json:"dns_upstream"`
	DNSFallback *string `json:"dns_fallback"`
}

// Validate checks all supplied values. Clearing an override (empty string)
// is always valid.
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
