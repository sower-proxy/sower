package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sower-proxy/sower/config"
)

func newGatedRequest(ip, user, password string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://gated.example.com/", nil)
	req.RemoteAddr = ip
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	return req
}

func TestSiteBasicAuthGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		user     string
		password string
		wantPass bool
		wantCode int
	}{
		{name: "valid credentials", user: "alice", password: "s3cret", wantPass: true},
		{name: "wrong password", user: "alice", password: "wrong", wantCode: http.StatusUnauthorized},
		{name: "wrong user", user: "bob", password: "s3cret", wantCode: http.StatusUnauthorized},
		{name: "missing credentials", wantCode: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate := newSiteBasicAuth(config.SiteRouteAuth{Username: "alice", Password: "s3cret"}, newAuthThrottle())
			if gate == nil {
				t.Fatal("configured credentials must build a gate")
			}
			req := newGatedRequest("203.0.113.9:1234", tt.user, tt.password)
			rec := httptest.NewRecorder()

			if got := gate.guard(rec, req); got != tt.wantPass {
				t.Fatalf("guard = %v, want %v (status %d)", got, tt.wantPass, rec.Code)
			}
			if tt.wantPass {
				if req.Header.Get("Authorization") != "" {
					t.Fatalf("Authorization survived a passed gate: %q", req.Header.Get("Authorization"))
				}
				return
			}
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="restricted"` {
				t.Fatalf("WWW-Authenticate = %q, want a Basic challenge", got)
			}
		})
	}
}

func TestSiteBasicAuthDisabledWithoutCredentials(t *testing.T) {
	t.Parallel()

	if gate := newSiteBasicAuth(config.SiteRouteAuth{}, newAuthThrottle()); gate != nil {
		t.Fatal("empty credentials must leave the route ungated")
	}
}

func TestSiteBasicAuthGuardLocksFailingSourceIP(t *testing.T) {
	t.Parallel()

	gate := newSiteBasicAuth(config.SiteRouteAuth{Username: "alice", Password: "s3cret"}, newAuthThrottle())

	const lockedIP = "203.0.113.9:1234"
	for i := 0; i < authMaxFails; i++ {
		rec := httptest.NewRecorder()
		req := newGatedRequest(lockedIP, "alice", "wrong")
		if gate.guard(rec, req) {
			t.Fatalf("attempt %d passed the gate", i)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i, rec.Code, http.StatusUnauthorized)
		}
	}

	// The lockout rejects even the correct password: that is the point.
	rec := httptest.NewRecorder()
	if gate.guard(rec, newGatedRequest(lockedIP, "alice", "s3cret")) {
		t.Fatal("locked source passed the gate")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}

	// A lockout is scoped to the source that earned it.
	other := httptest.NewRecorder()
	if !gate.guard(other, newGatedRequest("198.51.100.7:5678", "alice", "s3cret")) {
		t.Fatalf("unrelated source was rejected with status %d", other.Code)
	}
}
