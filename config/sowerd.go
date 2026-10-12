package config

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
)

const ExampleSowerdConfigTOML = `# Sowerd configuration example (TOML format)
# This is the daemon configuration for the sower server

# Logging configuration
log_level = "info" # Log level: debug, info, warn, error

# Server configuration
serve_ip = "0.0.0.0"              # IP address to listen on for ports 80 and 443
password = "change_me"            # Client auth password (required; replace the placeholder)
fake_site = "/var/www"            # Fallback fake site directory or address (default: /var/www)

# Site routing: map domains to upstream URLs for fallback traffic.
# When a TLS connection does not match sower transport,
# the SNI is looked up here. Unmatched connections fall through to fake_site.
# Custom certificate mode requires the certificate SANs to cover all routed domains.
# Route domains become public once autocert issues their certificates
# (Certificate Transparency), so an upstream that cannot authenticate its own
# visitors needs the optional auth gate below.
#
# [[site_routes]]
# domains = ["a.example.com", "b.example.com"]
# upstream = "http://127.0.0.1:8080"
#
# authority selects the Host header sent upstream: "upstream" (the default)
# sends the route's upstream host, "client" forwards the client's authority,
# for an upstream that validates that authority itself (see docs/security.md).
# authority = "client"
#
# [site_routes.auth]
# username = "admin"              # Both fields are required to enable the gate;
# password = "change_me"          # an empty block leaves the route open.
# The gate also covers WebSocket upgrades, but browsers cannot set Authorization
# on a WS handshake and reconnecting clients feed the per-IP lockout: verify
# before gating one (see docs/security.md).
#
# [site_routes.routes]
# "/ws" = "http://127.0.0.1:8082"
#
# [[site_routes]]
# domains = ["c.example.com"]
# upstream = "https://backend.example.com"
#
# URL userinfo credentials are sent to the upstream as HTTP Basic auth:
# upstream = "http://user:pass@127.0.0.1:8080"

# SSL/TLS certificate configuration
[cert]
email = "your-email@example.com" # Email for Let's Encrypt certificate
# domains = ["proxy.example.com"] # Issuance whitelist beyond site_routes (autocert mode).
# The FIRST entry answers TLS handshakes for unknown/empty SNI: keep it a concrete
# domain, not a wildcard. See docs/security.md.
cert = ""                        # Path to custom certificate file (optional)
key = ""                         # Path to custom private key file (optional)
`

// SiteRoute maps a set of exact domain names to an upstream URL.
// When a TLS connection's SNI matches one of the domains, fallback traffic
// is reverse-proxied to Upstream instead of going to fake_site. Routes
// override Upstream for requests whose path matches a configured prefix.
type SiteRoute struct {
	Domains  []string `usage:"exact domain names for this route"`
	Upstream string   `usage:"upstream URL (http:// or https://)"`
	// Authority selects the Host header presented to the upstream: the empty
	// value and AuthorityUpstream send the upstream host, AuthorityClient
	// forwards the authority the client asked for. Only an upstream that
	// validates that authority itself wants the client's name; the reasoning is
	// in docs/security.md.
	Authority string            `usage:"authority presented to the upstream: upstream (default) or client"`
	Routes    map[string]string `usage:"path-prefix to upstream URL overrides (e.g. /ws = http://127.0.0.1:8082)"`
	// Auth is an optional edge-side gate for visitors of this route. The
	// route domains are public (Certificate Transparency), so an upstream that
	// cannot authenticate its own visitors is exposed; the edge is the only
	// place to insert a gate because sowerd owns 80/443 on the host.
	Auth SiteRouteAuth `usage:"optional HTTP Basic visitor gate (leave empty to disable)"`
}

// SiteRouteAuth gates a site route behind HTTP Basic credentials. Both
// fields must be set to enable the gate; an empty block leaves the route
// open. The mechanism is deliberately HTTP Basic only: it needs no session
// state and works for non-browser clients, at the cost of a per-origin
// browser credential cache and no logout.
type SiteRouteAuth struct {
	Username string `usage:"visitor user name for the route's HTTP Basic gate"`
	Password string `usage:"visitor password for the route's HTTP Basic gate"`
}

// SowerdConfig represents the configuration for sowerd daemon
type SowerdConfig struct {
	LogLevel slog.Level `default:"info" usage:"log level: debug, info, warn, error"`
	ServeIP  string     `usage:"listen to port 80 443 of the IP"`
	// Password is compared byte-for-byte against the client-supplied value. It
	// stays a plain string (not deferlog.Password): the base64-or-plain
	// heuristic in deferlog.Password would silently corrupt passwords that
	// happen to be valid canonical base64, breaking client auth.
	Password   string      `required:"true"`
	FakeSite   string      `default:"/var/www" usage:"fake site address or directoy. serving on 127.0.0.1:80 if directory"`
	SiteRoutes []SiteRoute `usage:"domain-to-upstream routing rules for fallback traffic"`

	Cert struct {
		Email string
		Cert  string
		Key   string
		// Domains whitelists which names autocert may issue certificates
		// for, on top of every site_routes domain. A nil HostPolicy would
		// let anyone trigger issuance for arbitrary SNI names, exhausting
		// the Let's Encrypt account rate limits. Direct-connection domains
		// (e.g. the sower client's remote addr) must be listed here.
		Domains []string
	}
}

// Validate implements the validation interface for SowerdConfig
func (c *SowerdConfig) Validate() error {
	if c.ServeIP != "" && net.ParseIP(c.ServeIP) == nil {
		return fmt.Errorf("invalid serve ip: %q", c.ServeIP)
	}
	if c.Password == "" {
		return fmt.Errorf("password is required")
	}
	if c.FakeSite == "" {
		return fmt.Errorf("fake site is required")
	}

	if (c.Cert.Cert == "") != (c.Cert.Key == "") {
		return fmt.Errorf("cert and key must be configured together")
	}

	if err := validateSiteRoutes(c.SiteRoutes); err != nil {
		return err
	}

	seenCertDomains := make(map[string]struct{}, len(c.Cert.Domains))
	for _, d := range c.Cert.Domains {
		d = strings.ToLower(d)
		if !validExactDomain(d) {
			return fmt.Errorf("invalid cert domain %q", d)
		}
		if _, ok := seenCertDomains[d]; ok {
			return fmt.Errorf("duplicate cert domain %q", d)
		}
		seenCertDomains[d] = struct{}{}
	}

	if c.Cert.Cert != "" {
		cert, err := loadLeafCertificate(c.Cert.Cert)
		if err != nil {
			return err
		}
		if _, err := os.Stat(c.Cert.Key); err != nil {
			return fmt.Errorf("stat key file: %w", err)
		}
		if err := validateSiteRouteCertificate(cert, c.SiteRoutes); err != nil {
			return err
		}
	}

	if _, err := os.Stat(c.FakeSite); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat fake site: %w", err)
	}

	if _, _, err := net.SplitHostPort(c.FakeSite); err != nil {
		return fmt.Errorf("fake site must be an existing directory or host:port: %w", err)
	}

	return nil
}

// Authority values accepted by SiteRoute.Authority.
const (
	// AuthorityUpstream sends the route's upstream host as the Host header; it
	// is the default.
	AuthorityUpstream = "upstream"
	// AuthorityClient forwards the authority the client asked for.
	AuthorityClient = "client"
)

// PreserveHost reports whether the route asks the proxy to forward the client's
// authority instead of the upstream host.
func (r SiteRoute) PreserveHost() bool { return r.Authority == AuthorityClient }

// validateSiteRouteAuthority rejects a typo'd authority instead of silently
// falling back to the default, which is the value that hides the client's name.
func validateSiteRouteAuthority(i int, authority string) error {
	switch authority {
	case "", AuthorityUpstream, AuthorityClient:
		return nil
	}
	return fmt.Errorf("site_routes[%d].authority: unknown value %q (want %q or %q)", i, authority, AuthorityUpstream, AuthorityClient)
}

func validateSiteRoutes(routes []SiteRoute) error {
	seen := make(map[string]string) // domain -> upstream
	for i, r := range routes {
		if len(r.Domains) == 0 {
			return fmt.Errorf("site_routes[%d]: domains is empty", i)
		}
		if r.Upstream == "" {
			return fmt.Errorf("site_routes[%d]: upstream is empty", i)
		}

		if err := validateUpstreamURL(r.Upstream); err != nil {
			return fmt.Errorf("site_routes[%d]: %w", i, err)
		}

		if err := validateSiteRouteAuth(i, r.Auth); err != nil {
			return err
		}

		if err := validateSiteRouteAuthority(i, r.Authority); err != nil {
			return err
		}

		for path, upstream := range r.Routes {
			if !strings.HasPrefix(path, "/") {
				return fmt.Errorf("site_routes[%d].routes: path %q must start with /", i, path)
			}
			if path != strings.TrimSpace(path) {
				return fmt.Errorf("site_routes[%d].routes: path %q must not contain surrounding whitespace", i, path)
			}
			if strings.ContainsAny(path, "?#") {
				return fmt.Errorf("site_routes[%d].routes: path %q must not contain query or fragment", i, path)
			}
			if err := validateUpstreamURL(upstream); err != nil {
				return fmt.Errorf("site_routes[%d].routes[%q]: %w", i, path, err)
			}
		}

		for _, d := range r.Domains {
			if d != strings.TrimSpace(d) {
				return fmt.Errorf("site_routes[%d]: domain %q must not contain surrounding whitespace", i, d)
			}
			d = strings.ToLower(d)
			if strings.Contains(d, "*") {
				return fmt.Errorf("site_routes[%d]: wildcard domain %q is not supported", i, d)
			}
			if !validExactDomain(d) {
				return fmt.Errorf("site_routes[%d]: invalid domain %q", i, d)
			}
			if prev, ok := seen[d]; ok {
				return fmt.Errorf("site_routes: domain %q appears in multiple routes (%q and %q)", d, prev, r.Upstream)
			}
			seen[d] = r.Upstream
		}
	}
	return nil
}

func validateUpstreamURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse upstream: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("upstream must have a host")
	}
	return nil
}

// validateSiteRouteAuth rejects a half-configured visitor gate: a route
// whose credentials are only partly set would otherwise run either open or
// with a credential nobody can present. An empty block means "no gate".
func validateSiteRouteAuth(i int, auth SiteRouteAuth) error {
	if auth.Username == "" && auth.Password == "" {
		return nil
	}
	if auth.Username == "" || auth.Password == "" {
		return fmt.Errorf("site_routes[%d].auth: username and password must be set together", i)
	}
	if strings.ContainsRune(auth.Username, ':') {
		// RFC 7617 splits the decoded credential at the first colon.
		return fmt.Errorf("site_routes[%d].auth: username must not contain ':'", i)
	}
	return nil
}

func validExactDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || strings.HasSuffix(domain, ".") {
		return false
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func loadLeafCertificate(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cert file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("parse cert file %s: missing PEM certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cert file %s: %w", path, err)
	}
	return cert, nil
}

func validateSiteRouteCertificate(cert *x509.Certificate, routes []SiteRoute) error {
	for i, r := range routes {
		for _, domain := range r.Domains {
			domain = strings.ToLower(domain)
			if err := cert.VerifyHostname(domain); err != nil {
				return fmt.Errorf("site_routes[%d]: cert does not cover domain %q: %w", i, domain, err)
			}
		}
	}
	return nil
}
