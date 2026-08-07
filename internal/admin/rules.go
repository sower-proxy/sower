package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type rulesRequest struct {
	Category Category `json:"category"`
	Rules    []string `json:"rules"`
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
