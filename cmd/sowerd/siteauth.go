package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sower-proxy/sower/config"
)

// Visitor gate tuning. The throttle mirrors internal/admin's login throttle:
// failed attempts from one source IP accumulate (a passing request clears
// them) and lock that source out for a backoff window, and the failure map is
// bounded so source-IP rotation cannot grow it without bound. Without it a
// dictionary attack finishes in seconds, which would make the gate decorative.
const (
	authMaxFails     = 5
	authLockDuration = 15 * time.Minute
	authMaxEntries   = 4096
)

// siteBasicAuth gates a site route behind HTTP Basic credentials. It exists
// for upstreams that cannot authenticate their own visitors: the route
// domains are published in Certificate Transparency logs and the edge answers
// any SNI, so the upstream is reachable by anyone who reads those logs.
type siteBasicAuth struct {
	username string
	password string
	throttle *authThrottle
}

// newSiteBasicAuth builds the gate for one route, or nil when the route has no
// credentials configured.
func newSiteBasicAuth(conf config.SiteRouteAuth, throttle *authThrottle) *siteBasicAuth {
	if conf.Username == "" && conf.Password == "" {
		return nil
	}
	return &siteBasicAuth{username: conf.Username, password: conf.Password, throttle: throttle}
}

// guard reports whether the request passes the gate, writing the rejection
// response itself when it does not. A passing request has its Authorization
// header removed: the edge credential authenticates the visitor to the edge,
// not to the upstream, so forwarding it would leak the edge password into
// upstream logs and would collide with an upstream realm that carries the
// same name.
func (a *siteBasicAuth) guard(w http.ResponseWriter, r *http.Request) bool {
	ip := peerIP(r.RemoteAddr)
	if !a.throttle.allow(ip) {
		slog.Debug("site route auth locked", "host", r.Host, "remote", ip)
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return false
	}

	user, pass, ok := r.BasicAuth()
	userOK := secureEqual(user, a.username)
	passOK := secureEqual(pass, a.password)
	if !ok || !userOK || !passOK {
		a.throttle.fail(ip)
		slog.Debug("site route auth rejected", "host", r.Host, "remote", ip)
		w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}

	a.throttle.success(ip)
	r.Header.Del("Authorization")
	return true
}

// secureEqual compares two secrets in constant time regardless of length.
func secureEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// peerIP extracts the host part of a remote address for throttling. The TLS
// edge sees the direct peer, so the address is not attacker-controlled.
func peerIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// basicAuthHeader renders the Authorization value for the upstream
// credentials configured as URL userinfo on an upstream.
func basicAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

type authFail struct {
	count int
	until time.Time
}

// authThrottle bounds visitor password guessing per source IP. A lockout only
// ever affects the IP that caused it: there is no shared account state, and
// TCP requires a completed handshake, so a third party cannot lock anyone
// else out by spoofing their address.
type authThrottle struct {
	mu    sync.Mutex
	fails map[string]authFail
}

func newAuthThrottle() *authThrottle {
	return &authThrottle{fails: make(map[string]authFail)}
}

func (t *authThrottle) allow(ip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.fails[ip]
	return !ok || time.Now().After(f.until)
}

func (t *authThrottle) fail(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	// Bound the map: when full, drop entries whose lock window already
	// expired. If it is still full, stop tracking the new IP (it cannot be
	// locked, but the map stays bounded).
	if len(t.fails) >= authMaxEntries {
		for k, f := range t.fails {
			if now.After(f.until) {
				delete(t.fails, k)
			}
		}
	}
	if len(t.fails) >= authMaxEntries {
		return
	}
	f := t.fails[ip]
	f.count++
	if f.count >= authMaxFails {
		f.until = now.Add(authLockDuration)
		f.count = 0
	}
	t.fails[ip] = f
}

func (t *authThrottle) success(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, ip)
}
