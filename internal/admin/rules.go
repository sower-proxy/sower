package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type rulesRequest struct {
	Category Category `json:"category"`
	Rules    []string `json:"rules"`
}

// RuleSort selects the ordering of a rule listing.
type RuleSort string

const (
	RuleSortDefault  RuleSort = ""          // retained rule order (file order)
	RuleSortRule     RuleSort = "rule"      // alphabetical by rule text
	RuleSortHits     RuleSort = "hits"      // by hit count
	RuleSortLastSeen RuleSort = "last_seen" // by most recent hit
)

func (s RuleSort) valid() bool {
	switch s {
	case RuleSortDefault, RuleSortRule, RuleSortHits, RuleSortLastSeen:
		return true
	default:
		return false
	}
}

// SortDir selects the direction of a rule listing sort.
type SortDir string

const (
	SortDirAsc  SortDir = "asc"
	SortDirDesc SortDir = "desc"
)

func (d SortDir) valid() bool {
	return d == SortDirAsc || d == SortDirDesc
}

// defaultDir resolves the natural direction for a sort field: text sorts
// ascending, recency and magnitude sorts descending.
func defaultDir(s RuleSort) SortDir {
	if s == RuleSortRule {
		return SortDirAsc
	}
	return SortDirDesc
}

// RuleEntry is one retained rule in a listing, with its hit count and most
// recent hit time. Count is zero and LastSeen nil for rules that never
// matched a routed connection.
type RuleEntry struct {
	Rule     string     `json:"rule"`
	Count    uint64     `json:"count"`
	LastSeen *time.Time `json:"lastSeen,omitempty"`
}

// CategoryTest reports whether one rule category matched a tested domain and
// which retained rule did so.
type CategoryTest struct {
	Category Category `json:"category"`
	Matched  bool     `json:"matched"`
	Rule     string   `json:"rule"`
}

// DomainTest is the result of testing a domain against the rule sets. Route
// is block, direct, or proxy when a rule decides it, or auto when no rule
// matched and the connection would fall through to detection / proxy.
type DomainTest struct {
	Domain  string         `json:"domain"`
	Route   string         `json:"route"`
	Matches []CategoryTest `json:"matches"`
	Note    string         `json:"note,omitempty"`
}

// RuleHit aggregates block decisions attributed to one rule.
type RuleHit struct {
	Rule     string    `json:"rule"`
	Count    uint64    `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
}

// RuleMissProvider exposes per-domain access stats for connections that
// matched no block/direct/proxy rule. Rule holds the domain.
type RuleMissProvider interface {
	// RuleMiss returns up to limit domains, ordered by connection count
	// descending when byCount is true, or by most recent access otherwise.
	RuleMiss(byCount bool, limit int) []RuleHit
}

// handleRuleMiss serves per-domain access stats for connections that
// matched no rule, ordered by count (default) or by most recent access.
func (s *Server) handleRuleMiss(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.opts.Rules.(RuleMissProvider)
	if !ok {
		writeError(w, http.StatusNotFound, "rule miss stats unavailable")
		return
	}
	byCount := r.URL.Query().Get("sort") != "recent"
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, provider.RuleMiss(byCount, limit))
}

// handleRulesTest reports which rules match the queried domain and the
// resulting route decision, without performing any live detection.
func (s *Server) handleRulesTest(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "domain is required")
		return
	}
	res, err := s.opts.Rules.TestDomain(domain)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
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
	sortBy := RuleSort(r.URL.Query().Get("sort"))
	if !sortBy.valid() {
		sortBy = RuleSortDefault
	}
	dir := SortDir(r.URL.Query().Get("dir"))
	if !dir.valid() {
		dir = defaultDir(sortBy)
	}
	rules, total, err := s.opts.Rules.RuleSearch(category, q, offset, limit, sortBy, dir)
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
	if _, err := s.opts.Rules.RuleRemoveMany(req.Category, req.Rules...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRulesChanges returns the persisted rule deltas relative to the boot
// baseline, so the console can show what was customized and offer a reset.
func (s *Server) handleRulesChanges(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.opts.Rules.RuleChanges())
}

// handleRulesReset clears the deltas of one category — or of all categories
// when the body omits category — and rebuilds the runtime rule sets from the
// boot baseline.
func (s *Server) handleRulesReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Category Category `json:"category"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Category != "" && !req.Category.valid() {
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	if err := s.opts.Rules.RuleReset(req.Category); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
