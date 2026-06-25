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
password = "change_me"            # Password for client authentication
fake_site = "/var/www"            # Fallback fake site directory or address (default: /var/www)

# Site routing: map domains to upstream URLs for fallback traffic.
# When a TLS connection does not match sower transport,
# the SNI is looked up here. Unmatched connections fall through to fake_site.
# Custom certificate mode requires the certificate SANs to cover all routed domains.
#
# [[site_routes]]
# domains = ["a.example.com", "b.example.com"]
# upstream = "http://127.0.0.1:8080"
#
# [[site_routes]]
# domains = ["c.example.com"]
# upstream = "https://backend.example.com"

# SSL/TLS certificate configuration
[cert]
email = "your-email@example.com" # Email for Let's Encrypt certificate
cert = ""                        # Path to custom certificate file (optional)
key = ""                         # Path to custom private key file (optional)
`

// SiteRoute maps a set of exact domain names to an upstream URL.
// When a TLS connection's SNI matches one of the domains, fallback traffic
// is reverse-proxied to Upstream instead of going to fake_site.
type SiteRoute struct {
	Domains  []string `usage:"exact domain names for this route"`
	Upstream string   `usage:"upstream URL (http:// or https://)"`
}

// SowerdConfig represents the configuration for sowerd daemon
type SowerdConfig struct {
	LogLevel   slog.Level  `default:"info" usage:"log level: debug, info, warn, error"`
	ServeIP    string      `usage:"listen to port 80 443 of the IP"`
	Password   string      `required:"true"`
	FakeSite   string      `default:"/var/www" usage:"fake site address or directoy. serving on 127.0.0.1:80 if directory"`
	SiteRoutes []SiteRoute `usage:"domain-to-upstream routing rules for fallback traffic"`

	Cert struct {
		Email string
		Cert  string
		Key   string
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

func validateSiteRoutes(routes []SiteRoute) error {
	seen := make(map[string]string) // domain -> upstream
	for i, r := range routes {
		if len(r.Domains) == 0 {
			return fmt.Errorf("site_routes[%d]: domains is empty", i)
		}
		if r.Upstream == "" {
			return fmt.Errorf("site_routes[%d]: upstream is empty", i)
		}

		u, err := url.Parse(r.Upstream)
		if err != nil {
			return fmt.Errorf("site_routes[%d]: parse upstream: %w", i, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("site_routes[%d]: upstream scheme must be http or https, got %q", i, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("site_routes[%d]: upstream must have a host", i)
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
