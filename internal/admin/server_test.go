package admin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRules struct {
	lists map[Category][]string
	hits  []RuleHit
	miss  []RuleHit
	// errAdd makes RuleAdd fail, simulating a state persistence failure.
	errAdd error
}

func (f *fakeRules) RuleMiss(byCount bool, limit int) []RuleHit {
	out := append([]RuleHit(nil), f.miss...)
	sort.SliceStable(out, func(i, j int) bool {
		if byCount {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && limit < len(out) {
		return out[:limit]
	}
	return out
}

func newFakeRules() *fakeRules {
	return &fakeRules{lists: map[Category][]string{
		CategoryBlock:  {},
		CategoryDirect: {},
		CategoryProxy:  {},
	}}
}

func (f *fakeRules) RuleSearch(c Category, q string, offset, limit int, sortBy RuleSort, dir SortDir) ([]RuleEntry, uint64, error) {
	all := f.lists[c]
	matched := make([]RuleEntry, 0, len(all))
	q = strings.ToLower(q)
	for _, rule := range all {
		if q != "" && !strings.Contains(strings.ToLower(rule), q) {
			continue
		}
		e := RuleEntry{Rule: rule}
		for _, h := range f.hits {
			if h.Rule == rule {
				e.Count = h.Count
				last := h.LastSeen
				e.LastSeen = &last
				break
			}
		}
		matched = append(matched, e)
	}
	total := uint64(len(matched))
	switch sortBy {
	case RuleSortRule:
		sort.SliceStable(matched, func(i, j int) bool {
			if dir == SortDirAsc {
				return matched[i].Rule < matched[j].Rule
			}
			return matched[i].Rule > matched[j].Rule
		})
	case RuleSortHits:
		sort.SliceStable(matched, func(i, j int) bool {
			if dir == SortDirAsc {
				return matched[i].Count < matched[j].Count
			}
			return matched[i].Count > matched[j].Count
		})
	case RuleSortLastSeen:
		sort.SliceStable(matched, func(i, j int) bool {
			var ti, tj time.Time
			if matched[i].LastSeen != nil {
				ti = *matched[i].LastSeen
			}
			if matched[j].LastSeen != nil {
				tj = *matched[j].LastSeen
			}
			if dir == SortDirAsc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		})
	}
	if offset >= len(matched) {
		return []RuleEntry{}, total, nil
	}
	end := offset + limit
	if end > len(matched) {
		end = len(matched)
	}
	return matched[offset:end], total, nil
}

func (f *fakeRules) RuleAdd(c Category, rules ...string) error {
	if f.errAdd != nil {
		return f.errAdd
	}
	f.lists[c] = append(f.lists[c], rules...)
	return nil
}

func (f *fakeRules) RuleRemove(c Category, rule string) (bool, error) {
	for i, r := range f.lists[c] {
		if r == rule {
			f.lists[c] = append(f.lists[c][:i], f.lists[c][i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRules) RuleRemoveMany(c Category, rules ...string) ([]string, error) {
	removed := make([]string, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		if _, ok := seen[rule]; ok {
			continue
		}
		seen[rule] = struct{}{}
		if found, err := f.RuleRemove(c, rule); err != nil {
			return nil, err
		} else if found {
			removed = append(removed, rule)
		}
	}
	return removed, nil
}

func (f *fakeRules) RuleCount(c Category) uint64 {
	return uint64(len(f.lists[c]))
}

func (f *fakeRules) RuleChanges() RuleChangeSet {
	return RuleChangeSet{
		Persistent: true,
		Rules: map[Category]RuleDelta{
			CategoryBlock:  {Add: []string{}, Remove: []string{}},
			CategoryDirect: {Add: []string{}, Remove: []string{}},
			CategoryProxy:  {Add: []string{}, Remove: []string{}},
		},
	}
}

func (f *fakeRules) RuleReset(c Category) error {
	if c == "" {
		return nil
	}
	if !c.valid() {
		return fmt.Errorf("invalid category")
	}
	return nil
}

func (f *fakeRules) TestDomain(domain string) (DomainTest, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return DomainTest{}, fmt.Errorf("domain is required")
	}
	res := DomainTest{Domain: domain}
	for _, c := range []Category{CategoryBlock, CategoryDirect, CategoryProxy} {
		rule, ok := fakeRuleMatch(f.lists[c], domain)
		res.Matches = append(res.Matches, CategoryTest{Category: c, Matched: ok, Rule: rule})
		if ok && res.Route == "" {
			res.Route = string(c)
		}
	}
	if res.Route == "" {
		res.Route = "auto"
		res.Note = "no rule matched"
	}
	return res, nil
}

// fakeRuleMatch is the fake's linear matcher: exact match, "*", or "**." prefix.
func fakeRuleMatch(rules []string, item string) (string, bool) {
	for _, rule := range rules {
		switch {
		case rule == "*":
			return rule, true
		case strings.HasPrefix(rule, "**.") && strings.HasSuffix(item, strings.TrimPrefix(rule, "**")):
			return rule, true
		case rule == item:
			return rule, true
		}
	}
	return "", false
}

func newTestServer(t *testing.T, rules RuleManager) *httptest.Server {
	t.Helper()
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: rules, Stats: newTestStats(t)})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	return ts
}

// fakeConfig implements ConfigManager backed by a real StateStore, so PATCH
// revision semantics are exercised end to end.
type fakeConfig struct {
	state *StateStore
}

func newFakeConfig(t *testing.T) *fakeConfig {
	t.Helper()
	return &fakeConfig{state: LoadStateStore("")}
}

func (f *fakeConfig) ConfigView() ConfigView {
	return ConfigView{
		Revision: f.state.Revision(),
		Sections: []ConfigSection{
			{Name: "运行", Fields: []ConfigField{
				{Key: "log_level", Value: "info", Editable: true, ApplyMode: ApplyImmediate, Source: SourceConfig},
				{Key: "remote.password", ApplyMode: ApplyReadonly, Source: SourceConfig, Secret: true, Configured: true},
			}},
		},
	}
}

func (f *fakeConfig) ApplyConfigChanges(changes ConfigChanges, revision uint64) (ConfigView, error) {
	overrides := f.state.ConfigOverrides()
	if changes.LogLevel != nil {
		overrides.LogLevel = changes.LogLevel
	}
	if changes.DNSUpstream != nil {
		overrides.DNSUpstream = changes.DNSUpstream
	}
	if changes.DNSFallback != nil {
		overrides.DNSFallback = changes.DNSFallback
	}
	if _, err := f.state.ApplyConfig(overrides, revision); err != nil {
		return ConfigView{}, err
	}
	return f.ConfigView(), nil
}

func newTestConfigServer(t *testing.T, cfg ConfigManager) *httptest.Server {
	t.Helper()
	s := NewServer(Options{
		Password: "secret", Version: "v1.2.3", Date: "2026-01-01",
		Rules: newFakeRules(), Stats: newTestStats(t), Config: cfg,
	})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func login(t *testing.T, ts *httptest.Server, password string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/session", strings.NewReader(fmt.Sprintf(`{"password":%q}`, password)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login status: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			if c.HttpOnly == false || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie flags missing: HttpOnly=%v SameSite=%v", c.HttpOnly, c.SameSite)
			}
			return c.Value
		}
	}
	t.Fatal("login cookie not set")
	return ""
}

func authedRequest(t *testing.T, ts *httptest.Server, method, path, cookie, body string) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", sessionCookieName+"="+cookie)
	}
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func TestLoginInfoTemporaryPassword(t *testing.T) {
	s := NewServer(Options{Password: "generated", TemporaryPassword: true, Rules: newFakeRules()})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/login-info")
	if err != nil {
		t.Fatalf("get login info: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var out struct {
		TemporaryPassword bool `json:"temporaryPassword"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !out.TemporaryPassword {
		t.Fatal("expected temporaryPassword=true for a startup-generated password")
	}
}

func TestLoginInfoFixedPassword(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules()})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/login-info")
	if err != nil {
		t.Fatalf("get login info: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		TemporaryPassword bool `json:"temporaryPassword"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if out.TemporaryPassword {
		t.Fatal("expected temporaryPassword=false for a configured password")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	resp, err := http.Post(ts.URL+"/api/session", "application/json", strings.NewReader(`{"password":"wrong"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLoginRejectsMalformedJSON(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	resp, err := http.Post(ts.URL+"/api/session", "application/json", strings.NewReader(`{invalid`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAPIRequiresSession(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestSessionEndpoint(t *testing.T) {
	ts := newTestServer(t, newFakeRules())

	resp, err := http.Get(ts.URL + "/api/session")
	if err != nil {
		t.Fatalf("get session without cookie: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	cookie := login(t, ts, "secret")
	resp = authedRequest(t, ts, http.MethodGet, "/api/session", cookie, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for valid session, got %d", resp.StatusCode)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cacheControl)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName && c.MaxAge == int(sessionCookieMaxAge.Seconds()) {
			return
		}
	}
	t.Fatal("session probe did not renew the session cookie")
}

func TestStatusEndpoint(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodGet, "/api/status", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["version"] != "v1.2.3" {
		t.Fatalf("unexpected version: %v", body["version"])
	}
	rules := body["rules"].(map[string]any)
	if rules["proxy"] != float64(0) {
		t.Fatalf("unexpected rules: %v", rules)
	}
}

// TestRulesAddPersistFailureReturns500 pins the contract that a state
// persistence failure surfaces as a 500 instead of a silent 204, so the
// console cannot pretend a rule change landed when it was not written.
func TestRulesAddPersistFailureReturns500(t *testing.T) {
	fr := newFakeRules()
	fr.errAdd = errors.New("persist rule additions: write /tmp/admin-state.json: no space left")
	ts := newTestServer(t, fr)
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie,
		`{"category":"proxy","rules":["example.com"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("persist failure must yield 500, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "persist") {
		t.Fatalf("error must surface the persist failure, got %q", msg)
	}
}

func TestRulesCRUD(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")
	ruleOf := func(v any) string { return v.(map[string]any)["rule"].(string) }

	// list empty
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy", cookie, "")
	body := decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 0 {
		t.Fatalf("expected empty rules, got %v", rules)
	}

	// add returns 204 and the list reflects the addition
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["example.com","**.cn"]}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("add status: %d", resp.StatusCode)
	}
	resp.Body.Close()
	body = decodeBody(t, authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy", cookie, ""))
	if rules, _ := body["rules"].([]any); len(rules) != 2 {
		t.Fatalf("expected 2 rules after add, got %v", rules)
	}

	// remove returns 204 and the list reflects the removal
	resp = authedRequest(t, ts, http.MethodDelete, "/api/rules", cookie, `{"category":"proxy","rules":["example.com"]}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove status: %d", resp.StatusCode)
	}
	resp.Body.Close()
	body = decodeBody(t, authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy", cookie, ""))
	if rules, _ := body["rules"].([]any); len(rules) != 1 || ruleOf(rules[0]) != "**.cn" {
		t.Fatalf("unexpected rules after remove: %v", rules)
	}
}

func TestRulesListPaginationAndSearch(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	for _, rule := range []string{"a.com", "b.cn", "a.cn", "github.com", "mail.com"} {
		resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie,
			fmt.Sprintf(`{"category":"proxy","rules":[%q]}`, rule))
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add %s: %d", rule, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// pagination
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&offset=1&limit=2", cookie, "")
	body := decodeBody(t, resp)
	ruleOf := func(v any) string { return v.(map[string]any)["rule"].(string) }
	if rules, _ := body["rules"].([]any); len(rules) != 2 || ruleOf(rules[0]) != "b.cn" || ruleOf(rules[1]) != "a.cn" {
		t.Fatalf("unexpected page: %v", rules)
	}
	if total, _ := body["total"].(float64); total != 5 {
		t.Fatalf("expected total 5, got %v", total)
	}

	// fuzzy search (case-insensitive substring)
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&q=CN", cookie, "")
	body = decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 2 {
		t.Fatalf("expected 2 matches for CN, got %v", rules)
	}

	// search + pagination
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&q=cn&offset=1&limit=1", cookie, "")
	body = decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 1 || ruleOf(rules[0]) != "a.cn" {
		t.Fatalf("unexpected search page: %v", rules)
	}

	// limit clamp
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&limit=99999", cookie, "")
	body = decodeBody(t, resp)
	if limit, _ := body["limit"].(float64); limit != maxPageSize {
		t.Fatalf("expected limit clamped to %d, got %v", maxPageSize, limit)
	}
}

func TestRulesListSortByHits(t *testing.T) {
	rules := newFakeRules()
	rules.hits = []RuleHit{
		{Rule: "b.com", Count: 5},
		{Rule: "a.com", Count: 3},
	}
	ts := newTestServer(t, rules)
	cookie := login(t, ts, "secret")
	for _, rule := range []string{"a.com", "b.com", "c.com"} {
		resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie,
			fmt.Sprintf(`{"category":"proxy","rules":[%q]}`, rule))
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add %s: %d", rule, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp := authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&sort=hits", cookie, "")
	defer resp.Body.Close()
	var page rulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := make([]string, len(page.Rules))
	counts := make([]uint64, len(page.Rules))
	for i, e := range page.Rules {
		got[i] = e.Rule
		counts[i] = e.Count
	}
	want := []string{"b.com", "a.com", "c.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected hit ordering: %v", got)
	}
	if counts[0] != 5 || counts[1] != 3 || counts[2] != 0 {
		t.Fatalf("unexpected counts: %v", counts)
	}

	// unknown sort falls back to default order
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&sort=bogus", cookie, "")
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Rules[0].Rule != "a.com" {
		t.Fatalf("expected default order for bogus sort, got %+v", page.Rules)
	}
}

func TestRulesListSorts(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)
	rules := newFakeRules()
	rules.hits = []RuleHit{
		{Rule: "b.com", Count: 5, LastSeen: t1},
		{Rule: "a.com", Count: 3, LastSeen: t2},
	}
	ts := newTestServer(t, rules)
	cookie := login(t, ts, "secret")
	for _, rule := range []string{"b.com", "c.com", "a.com"} {
		resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie,
			fmt.Sprintf(`{"category":"proxy","rules":[%q]}`, rule))
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("add %s: %d", rule, resp.StatusCode)
		}
		resp.Body.Close()
	}

	orderOf := func(path string) []string {
		t.Helper()
		resp := authedRequest(t, ts, http.MethodGet, path, cookie, "")
		defer resp.Body.Close()
		var page rulesResponse
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got := make([]string, len(page.Rules))
		for i, e := range page.Rules {
			got[i] = e.Rule
		}
		return got
	}
	join := func(got []string) string { return strings.Join(got, ",") }

	// rule sort defaults to ascending, honors explicit direction
	if got := orderOf("/api/rules?category=proxy&sort=rule"); join(got) != "a.com,b.com,c.com" {
		t.Fatalf("rule asc: %v", got)
	}
	if got := orderOf("/api/rules?category=proxy&sort=rule&dir=desc"); join(got) != "c.com,b.com,a.com" {
		t.Fatalf("rule desc: %v", got)
	}

	// hits asc puts zero-count rules first, ascending counts behind
	if got := orderOf("/api/rules?category=proxy&sort=hits&dir=asc"); join(got) != "c.com,a.com,b.com" {
		t.Fatalf("hits asc: %v", got)
	}

	// last_seen defaults to most recent first and serializes the timestamp
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy&sort=last_seen", cookie, "")
	defer resp.Body.Close()
	var page rulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Rules[0].Rule != "a.com" || page.Rules[1].Rule != "b.com" {
		t.Fatalf("last_seen order: %+v", page.Rules)
	}
	if page.Rules[0].LastSeen == nil || !page.Rules[0].LastSeen.Equal(t2) {
		t.Fatalf("lastSeen not serialized: %+v", page.Rules[0])
	}
}

func TestRulesRejectsInvalidCategory(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	for _, path := range []string{"/api/rules?category=bogus"} {
		resp := authedRequest(t, ts, http.MethodGet, path, cookie, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"bogus","rules":["a.com"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for add with bad category, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRulesChangesAndResetEndpoints(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	// changes reports all three categories and the persistence flag
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules/changes", cookie, "")
	body := decodeBody(t, resp)
	if persistent, _ := body["persistent"].(bool); !persistent {
		t.Fatalf("expected persistent=true, got %v", body["persistent"])
	}
	rules, _ := body["rules"].(map[string]any)
	for _, cat := range []string{"block", "direct", "proxy"} {
		if _, ok := rules[cat]; !ok {
			t.Fatalf("changes missing category %s: %v", cat, rules)
		}
	}

	// changes requires a session
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/changes", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// reset rejects an invalid category
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules/reset", cookie, `{"category":"bogus"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for reset with bad category, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// reset one category, then all
	for _, payload := range []string{`{"category":"proxy"}`, `{}`} {
		resp = authedRequest(t, ts, http.MethodPost, "/api/rules/reset", cookie, payload)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204 for reset %s, got %d", payload, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestRulesRejectsInvalidBody(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	// unknown field
	resp := authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["a.com"],"extra":1}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// empty rule list
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":[]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty rules, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// whitespace-only rule
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["   "]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for whitespace rule, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// rule longer than the max rule length
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["`+strings.Repeat("a", 300)+`"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized rule, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// oversized body
	big := strings.Repeat("a", 70<<10)
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["`+big+`"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCrossOriginMutationRejected(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/rules", strings.NewReader(`{"category":"proxy","rules":["a.com"]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookieName+"="+cookie)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin mutation, got %d", resp.StatusCode)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodDelete, "/api/session", cookie, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status: %d", resp.StatusCode)
	}
	cookieExpired := false
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cookieExpired = true
		}
	}
	resp.Body.Close()
	if !cookieExpired {
		t.Fatal("logout did not expire the session cookie")
	}

	resp = authedRequest(t, ts, http.MethodGet, "/api/status", cookie, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTrafficEndpoint(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	cookie := login(t, ts, "secret")
	stats.RecordDNS("example.com.", "")
	stats.WrapConn(&fakeConn{}, "socks5")

	resp := authedRequest(t, ts, http.MethodGet, "/api/traffic", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["dnsQueries"] != float64(1) {
		t.Fatalf("unexpected dnsQueries: %v", body["dnsQueries"])
	}
	if body["uptime"] == nil {
		t.Fatal("expected uptime field")
	}
}

func TestTotalsEndpoint(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	cookie := login(t, ts, "secret")
	stats.RecordDNS("example.com.", "192.168.1.10")

	resp := authedRequest(t, ts, http.MethodGet, "/api/totals", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if domains, ok := body["domains"].([]any); !ok || len(domains) != 1 {
		t.Fatalf("expected 1 domain in totals, got %v", body["domains"])
	}
	if clients, ok := body["clients"].([]any); !ok || len(clients) != 1 {
		t.Fatalf("expected 1 client in totals, got %v", body["clients"])
	}
}

func TestRulesTestEndpoint(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules()})
	if err := s.opts.Rules.RuleAdd(CategoryBlock, "ads.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.opts.Rules.RuleAdd(CategoryProxy, "**.example.org"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodGet, "/api/rules/test?domain=ads.example.com", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["domain"] != "ads.example.com" || body["route"] != "block" {
		t.Fatalf("unexpected test result: %v", body)
	}
	matches := body["matches"].([]any)
	first := matches[0].(map[string]any)
	if first["category"] != "block" || first["matched"] != true || first["rule"] != "ads.example.com" {
		t.Fatalf("unexpected block match: %v", first)
	}

	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/test?domain=sub.example.org", cookie, "")
	body = decodeBody(t, resp)
	if body["route"] != "proxy" {
		t.Fatalf("expected proxy route for wildcard match, got %v", body)
	}

	// no rules matched: auto with a note, block takes priority over proxy
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/test?domain=unknown.test", cookie, "")
	body = decodeBody(t, resp)
	if body["route"] != "auto" {
		t.Fatalf("expected auto route, got %v", body)
	}

	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/test", cookie, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without domain, got %d", resp.StatusCode)
	}
}

func TestRuleMissEndpoint(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)
	rules := newFakeRules()
	rules.miss = []RuleHit{
		{Rule: "baidu.com", Count: 5, LastSeen: t1},
		{Rule: "qq.com", Count: 3, LastSeen: t2},
	}
	s := NewServer(Options{Password: "secret", Rules: rules})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	cookie := login(t, ts, "secret")

	// Default sort: by connection count, descending.
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules/miss", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var miss []RuleHit
	if err := json.NewDecoder(resp.Body).Decode(&miss); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(miss) != 2 || miss[0].Rule != "baidu.com" || miss[0].Count != 5 {
		t.Fatalf("unexpected miss order: %+v", miss)
	}

	// Recent sort: by most recent access, descending.
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/miss?sort=recent", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&miss); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(miss) != 2 || miss[0].Rule != "qq.com" {
		t.Fatalf("unexpected recent order: %+v", miss)
	}

	// Limit clamp.
	resp = authedRequest(t, ts, http.MethodGet, "/api/rules/miss?limit=1", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&miss); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if len(miss) != 1 || miss[0].Rule != "baidu.com" {
		t.Fatalf("unexpected limited miss: %+v", miss)
	}
}

// ruleManagerNoHits wraps a RuleManager to hide the RuleHitProvider
// implementation, exercising the 404 path of the hits endpoint.
type ruleManagerNoHits struct {
	RuleManager
}

// TestRuleHitsUnavailable guards the 404 path when the RuleManager does not
// implement the hit provider (e.g. an alternative adapter).
func TestRuleHitsUnavailable(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: ruleManagerNoHits{newFakeRules()}})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodGet, "/api/rules/hits", cookie, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 without provider, got %d", resp.StatusCode)
	}
}

func TestTrafficEndpointRejectsMissingStats(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules()})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodGet, "/api/traffic", cookie, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 when stats are unavailable, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["error"] != "stats unavailable" {
		t.Fatalf("unexpected error: %v", body)
	}
}

func TestStaticServesIndexAndSPAFallback(t *testing.T) {
	ts := newTestServer(t, newFakeRules())

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// SPA fallback for client-side routes
	resp, err = http.Get(ts.URL + "/some/client/route")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for SPA route, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUnknownAPIPathReturnsJSONNotFound(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	for _, path := range []string{"/api", "/api/missing"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			resp.Body.Close()
			t.Fatalf("expected 404 for %s, got %d", path, resp.StatusCode)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			resp.Body.Close()
			t.Fatalf("expected JSON response for %s, got %q", path, resp.Header.Get("Content-Type"))
		}
		body := decodeBody(t, resp)
		if body["error"] != "unknown API endpoint" {
			t.Fatalf("unexpected error for %s: %v", path, body)
		}
	}
}

func TestShutdown(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t)})
	ts := httptest.NewServer(s.http.Handler)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	ts.Close()
}

func TestHistoryEndpoint(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	defer ts.Close()

	cookie := login(t, ts, "secret")
	resp := authedRequest(t, ts, http.MethodGet, "/api/history", cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Samples []HistorySample `json:"samples"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Samples == nil {
		t.Fatal("expected samples array")
	}
}

func TestHistoryEndpointRequiresAuth(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/history")
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
}

func TestMetricsEndpointServesPrometheusFormat(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	defer ts.Close()

	stats.RecordDNS("example.com", "")
	stats.RecordRoute("proxy", "")
	conn := stats.WrapConn(&fakeConn{}, "https")
	_ = conn.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{
		"sower_dns_queries_total 1",
		`sower_rule_hits_total{route="proxy"} 1`,
		`sower_connections_active{protocol="https"} 0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in metrics, got:\n%s", want, body)
		}
	}
}

// TestMetricsBytesCountedOncePerFlush guards the OTel byte counter against
// double counting: each flush must record the batch exactly once, and bytes
// read before BindConn (e.g. TLS ClientHello) must still be counted.
func TestMetricsBytesCountedOncePerFlush(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	defer ts.Close()

	conn := stats.WrapConn(&fakeConn{readBuf: bytes.Repeat([]byte("x"), 300)}, "https")
	pre := make([]byte, 100) // protocol bytes read before the domain is known
	if _, err := conn.Read(pre); err != nil {
		t.Fatalf("read: %v", err)
	}
	stats.BindConn(conn, "example.com")
	payload := make([]byte, 200)
	if _, err := conn.Read(payload); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := conn.Write(bytes.Repeat([]byte("y"), 1000)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.Close() // drains the pending batches

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	for _, want := range []string{
		`sower_bytes_total{direction="up"} 300`,
		`sower_bytes_total{direction="down"} 1000`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("expected %q in metrics, got:\n%s", want, body)
		}
	}
}

func TestSessionSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sessions.json")
	newServer := func() *httptest.Server {
		s := NewServer(Options{
			Password: "secret", Version: "v1.2.3", Date: "2026-01-01",
			Rules: newFakeRules(), Stats: newTestStats(t), SessionFile: file,
		})
		ts := httptest.NewServer(s.http.Handler)
		t.Cleanup(ts.Close)
		return ts
	}

	ts1 := newServer()
	cookie := login(t, ts1, "secret")

	// simulate a process restart: a frontend-style session probe on the new
	// server must accept the persisted cookie before rendering the console.
	ts2 := newServer()
	resp := authedRequest(t, ts2, http.MethodGet, "/api/session", cookie, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected session probe to survive restart, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// the persisted file must be private
	fi, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected session file 0600, got %v", fi.Mode().Perm())
	}

	// logout on the new server must revoke the persisted session
	resp = authedRequest(t, ts2, http.MethodDelete, "/api/session", cookie, "")
	resp.Body.Close()
	resp = authedRequest(t, ts2, http.MethodGet, "/api/status", cookie, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected logout to revoke session, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSessionSlidingExpiry(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t)})
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sessions["token"] = time.Now().Add(sessionTTL / 4) // less than half TTL remaining
	}()
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	resp := authedRequest(t, ts, http.MethodGet, "/api/session", "token", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected renewed session probe to return 204, got %d", resp.StatusCode)
	}
	renewedCookie := false
	for _, c := range resp.Cookies() {
		if c.Name != sessionCookieName {
			continue
		}
		if c.MaxAge != int(sessionCookieMaxAge.Seconds()) {
			t.Fatalf("renewed cookie MaxAge = %d, want %d", c.MaxAge, int(sessionCookieMaxAge.Seconds()))
		}
		renewedCookie = true
	}
	if !renewedCookie {
		t.Fatal("session renewal did not emit a cookie")
	}

	var exp time.Time
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		exp = s.sessions["token"]
	}()
	if remaining := time.Until(exp); remaining < sessionTTL/2 {
		t.Fatalf("expected sliding expiry to refresh TTL, remaining %v", remaining)
	}
}

func TestLoginSucceedsWhenSessionPersistenceFails(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write persistence blocker: %v", err)
	}
	s := NewServer(Options{
		Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t),
		SessionFile: filepath.Join(notDir, "sessions.json"),
	})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/session", "application/json", strings.NewReader(`{"password":"secret"}`))
	if err != nil {
		t.Fatalf("login with unwritable persistence: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 despite persistence failure, got %d", resp.StatusCode)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login did not issue a session cookie")
	}
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.sessions[cookie]; !ok {
			t.Fatal("login did not retain the session in memory")
		}
	}()

	// The in-memory session must still authenticate API requests.
	authed := authedRequest(t, ts, http.MethodGet, "/api/session", cookie, "")
	defer authed.Body.Close()
	if authed.StatusCode != http.StatusNoContent {
		t.Fatalf("expected in-memory session to authenticate, got %d", authed.StatusCode)
	}
}

func TestLogoutRevokesMemoryWhenPersistenceFails(t *testing.T) {
	notDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write persistence blocker: %v", err)
	}
	s := NewServer(Options{
		Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t),
		SessionFile: filepath.Join(notDir, "sessions.json"),
	})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	cookie := login(t, ts, "secret")
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/session", nil)
	if err != nil {
		t.Fatalf("build logout request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("logout with unwritable persistence: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 despite persistence failure, got %d", resp.StatusCode)
	}
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.sessions[cookie]; ok {
			t.Fatal("logout left the session valid in memory")
		}
	}()
}

func TestLoginCookieSecureFromConfig(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), CookieSecure: true})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/session", "application/json", strings.NewReader(`{"password":"secret"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			if !c.Secure {
				t.Fatal("expected Secure cookie from CookieSecure option")
			}
			return
		}
	}
	t.Fatal("login cookie not set")
}

func TestExpiredSessionsDroppedOnLoad(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sessions.json")
	now := time.Now()
	data, err := json.Marshal(map[string]time.Time{
		"expired": now.Add(-time.Minute),
		"alive":   now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t), SessionFile: file})
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions["expired"]; ok {
		t.Fatal("expected expired session to be dropped on load")
	}
	if _, ok := s.sessions["alive"]; !ok {
		t.Fatal("expected live session to be restored")
	}
}

func TestStreamEndpointRequiresAuth(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/stream")
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
}

func TestStreamEndpointPushesEvents(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	defer ts.Close()

	cookie := login(t, ts, "secret")
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/stream?source=dns", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Cookie", sessionCookieName+"="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	type line struct{ text string }
	lines := make(chan line, 8)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			lines <- line{scanner.Text()}
		}
	}()

	saw := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !saw["status"] || !saw["traffic"] || !saw["history"] {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events: %v", saw)
		case l := <-lines:
			if prefix, ok := strings.CutPrefix(l.text, "event: "); ok {
				saw[prefix] = true
			}
		}
	}
}

func TestStreamSessionRenewalClosesWithoutAuth(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	cookie := login(t, ts, "secret")
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("new stream request: %v", err)
	}
	req.Header.Set("Cookie", sessionCookieName+"="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	events := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if event, ok := strings.CutPrefix(scanner.Text(), "event: "); ok {
				events <- event
			}
		}
	}()

	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.sessions[cookie] = time.Now().Add(sessionTTL / 4)
	}()

	deadline := time.After(8 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("stream closed before sending renewal event")
			}
			switch event {
			case "auth":
				t.Fatal("renewal emitted auth event")
			case "renew":
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("stream stayed open after renewal event")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for session renewal")
		}
	}
}

func TestStreamDefaultTrafficSnapshotCacheConcurrent(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: stats})

	var wg sync.WaitGroup
	errors := make(chan string, 16)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				snap := s.streamTrafficSnapshot(DomainSortBytes, SourceAll, "")
				if snap.DNSQueries != 0 {
					errors <- fmt.Sprintf("unexpected DNS queries %d in cached view", snap.DNSQueries)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

func TestStreamDefaultTrafficSnapshotCache(t *testing.T) {
	stats := newTestStats(t)
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: stats})

	first := s.streamTrafficSnapshot(DomainSortBytes, SourceAll, "")
	stats.RecordDNS("fresh.example", "")
	cached := s.streamTrafficSnapshot(DomainSortBytes, SourceAll, "")
	if cached.DNSQueries != first.DNSQueries {
		t.Fatalf("expected cached snapshot, got DNS queries %d after %d", cached.DNSQueries, first.DNSQueries)
	}

	s.trafficMu.Lock()
	s.traffic.at = time.Now().Add(-trafficCacheTTL)
	s.trafficMu.Unlock()
	refreshed := s.streamTrafficSnapshot(DomainSortBytes, SourceAll, "")
	if refreshed.DNSQueries != first.DNSQueries+1 {
		t.Fatalf("expected refreshed snapshot with new DNS query, got %d", refreshed.DNSQueries)
	}

	stats.RecordDNS("filtered.example", "")
	filtered := s.streamTrafficSnapshot(DomainSortRecent, SourceAll, "")
	if filtered.DNSQueries != refreshed.DNSQueries+1 {
		t.Fatalf("expected filtered snapshot to bypass cache, got %d", filtered.DNSQueries)
	}
}

// failingFlusher is a ResponseWriter whose flush can be made to fail on
// demand, letting the stream handler's write-failure path be exercised
// without a real socket. The first flush errors like a dead connection
// after the initial payload has been delivered.
type failingFlusher struct {
	header  http.Header
	flushed atomic.Int32
	fail    atomic.Bool
}

func (f *failingFlusher) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}
func (*failingFlusher) WriteHeader(int)             {}
func (*failingFlusher) Write(p []byte) (int, error) { return len(p), nil }

// FlushError is the error-returning form http.ResponseController.Flush
// looks for (Go 1.26+); it lets the test fail flushes on demand.
func (f *failingFlusher) FlushError() error {
	f.flushed.Add(1)
	if f.fail.Load() {
		return errors.New("flush failed")
	}
	return nil
}

// TestStreamHandlerExitsOnWriteFailure pins that a dead connection cannot
// hold the handler open: once a write fails, the stream is torn down instead
// of looping forever on a client that vanished without closing the TCP
// connection.
func TestStreamHandlerExitsOnWriteFailure(t *testing.T) {
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: newTestStats(t)})
	s.mu.Lock()
	s.sessions["token"] = time.Now().Add(time.Hour)
	s.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "token"})
	w := &failingFlusher{}

	done := make(chan struct{})
	go func() {
		s.handleStream(w, req)
		close(done)
	}()

	// The initial payload goes out while writes succeed.
	deadline := time.After(2 * time.Second)
	for w.flushed.Load() < 5 { // retry line + four initial events
		select {
		case <-deadline:
			t.Fatalf("initial payload not flushed after 2s (flushed=%d)", w.flushed.Load())
		case <-done:
			t.Fatal("stream exited before the write failure")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// The next tick write fails, so the handler must return instead of
	// blocking forever on the dead connection.
	w.fail.Store(true)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stream handler stayed alive after write failures")
	}
}

func TestConfigGetSanitizesSecrets(t *testing.T) {
	ts := newTestConfigServer(t, newFakeConfig(t))
	cookie := login(t, ts, "secret")

	resp := authedRequest(t, ts, http.MethodGet, "/api/config", cookie, "")
	body := decodeBody(t, resp)
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "secret") && strings.Contains(string(raw), "password\":") {
		t.Fatalf("secret value leaked in config view: %s", raw)
	}
	// The password field must surface as metadata, not a value.
	if !strings.Contains(string(raw), `"secret":true`) || !strings.Contains(string(raw), `"configured":true`) {
		t.Fatalf("expected secret metadata, got %s", raw)
	}

	// config requires a session
	resp = authedRequest(t, ts, http.MethodGet, "/api/config", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestConfigPatchValidationAndRevision(t *testing.T) {
	fake := newFakeConfig(t)
	ts := newTestConfigServer(t, fake)
	cookie := login(t, ts, "secret")

	// invalid log level
	resp := authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":0,"changes":{"log_level":"bogus"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid log_level, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// invalid DNS IP
	resp = authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":0,"changes":{"dns_upstream":"not-an-ip"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid dns_upstream, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// unknown fields are rejected by the strict decoder
	resp = authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":0,"changes":{"remote_password":"x"}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown change field, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// stale revision
	resp = authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":7,"changes":{"log_level":"debug"}}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for stale revision, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// happy path: revision bumps, override persisted
	resp = authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":0,"changes":{"log_level":"debug","dns_upstream":"1.1.1.1"}}`)
	body := decodeBody(t, resp)
	if rev, _ := body["revision"].(float64); rev != 1 {
		t.Fatalf("expected revision 1 after patch, got %v", body["revision"])
	}
	if got := fake.state.ConfigOverrides(); got.LogLevel == nil || *got.LogLevel != "debug" || got.DNSUpstream == nil || *got.DNSUpstream != "1.1.1.1" {
		t.Fatalf("overrides not persisted: %+v", got)
	}

	// clearing an override with an empty string is valid
	resp = authedRequest(t, ts, http.MethodPatch, "/api/config", cookie,
		`{"revision":1,"changes":{"dns_upstream":""}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for clearing override, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := fake.state.ConfigOverrides(); got.DNSUpstream == nil || *got.DNSUpstream != "" {
		t.Fatalf("override not cleared: %+v", got)
	}
}
