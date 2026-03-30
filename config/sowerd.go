package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
)

const ExampleSowerdConfigTOML = `# Sowerd configuration example (TOML format)
# This is the daemon configuration for the sower server

# Logging configuration
log_level = "info" # Log level: debug, info, warn, error

# Server configuration
serve_ip = "0.0.0.0"              # IP address to listen on for ports 80 and 443
password = "change_me"            # Password for client authentication
fake_site = "/var/www"            # Fake site directory or address (default: /var/www)

# SSL/TLS certificate configuration
[cert]
email = "your-email@example.com" # Email for Let's Encrypt certificate
cert = ""                        # Path to custom certificate file (optional)
key = ""                         # Path to custom private key file (optional)
`

// SowerdConfig represents the configuration for sowerd daemon
type SowerdConfig struct {
	LogLevel slog.Level `default:"info" usage:"log level: debug, info, warn, error"`
	ServeIP  string     `usage:"listen to port 80 443 of the IP"`
	Password string     `required:"true"`
	FakeSite string     `default:"/var/www" usage:"fake site address or directoy. serving on 127.0.0.1:80 if directory"`

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

	if c.Cert.Cert != "" {
		if _, err := os.Stat(c.Cert.Cert); err != nil {
			return fmt.Errorf("stat cert file: %w", err)
		}
		if _, err := os.Stat(c.Cert.Key); err != nil {
			return fmt.Errorf("stat key file: %w", err)
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
