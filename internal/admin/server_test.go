package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRules struct {
	lists map[Category][]string
}

func newFakeRules() *fakeRules {
	return &fakeRules{lists: map[Category][]string{
		CategoryBlock:  {},
		CategoryDirect: {},
		CategoryProxy:  {},
	}}
}

func (f *fakeRules) RuleList(c Category) ([]string, error) {
	return append([]string(nil), f.lists[c]...), nil
}

func (f *fakeRules) RuleAdd(c Category, rules ...string) error {
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

func (f *fakeRules) RuleCount(c Category) uint64 {
	return uint64(len(f.lists[c]))
}

func newTestServer(t *testing.T, rules RuleManager) *httptest.Server {
	t.Helper()
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: rules, Stats: NewStats()})
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

func TestRulesCRUD(t *testing.T) {
	ts := newTestServer(t, newFakeRules())
	cookie := login(t, ts, "secret")

	// list empty
	resp := authedRequest(t, ts, http.MethodGet, "/api/rules?category=proxy", cookie, "")
	body := decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 0 {
		t.Fatalf("expected empty rules, got %v", rules)
	}

	// add
	resp = authedRequest(t, ts, http.MethodPost, "/api/rules", cookie, `{"category":"proxy","rules":["example.com","**.cn"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add status: %d", resp.StatusCode)
	}
	body = decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 2 {
		t.Fatalf("expected 2 rules after add, got %v", rules)
	}

	// remove
	resp = authedRequest(t, ts, http.MethodDelete, "/api/rules", cookie, `{"category":"proxy","rules":["example.com"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove status: %d", resp.StatusCode)
	}
	body = decodeBody(t, resp)
	if rules, _ := body["rules"].([]any); len(rules) != 1 || rules[0] != "**.cn" {
		t.Fatalf("unexpected rules after remove: %v", rules)
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
	stats := NewStats()
	s := NewServer(Options{Password: "secret", Version: "v1.2.3", Date: "2026-01-01", Rules: newFakeRules(), Stats: stats})
	ts := httptest.NewServer(s.http.Handler)
	t.Cleanup(ts.Close)

	cookie := login(t, ts, "secret")
	stats.RecordDNS("example.com.")
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
	s := NewServer(Options{Password: "secret", Rules: newFakeRules(), Stats: NewStats()})
	ts := httptest.NewServer(s.http.Handler)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	ts.Close()
}
