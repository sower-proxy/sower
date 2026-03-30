package config

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/sower-proxy/sower/pkg/upstreamtls"
)

type RemoteTLSConfig struct {
	ServerName         string `usage:"override upstream TLS server name (SNI)"`
	ClientHello        string `usage:"uTLS client hello: chrome, firefox, ios, android, edge, safari, 360, qq, randomized, randomized_alpn, randomized_no_alpn, golang"`
	InsecureSkipVerify bool   `default:"false" usage:"skip upstream TLS certificate verification"`
}

type RemoteConfig struct {
	Type     string          `default:"sower" required:"true" usage:"option: sower/trojan/socks5"`
	Addr     string          `required:"true" usage:"proxy address, eg: proxy.com or proxy.com:443"`
	Password string          `usage:"remote proxy password"`
	TLS      RemoteTLSConfig `flag:"tls"`
}

// SowerConfig represents the configuration for sower client
type SowerConfig struct {
	LogLevel slog.Level `default:"info" usage:"log level: debug, info, warn, error"`

	Remote RemoteConfig

	DNS struct {
		Disable    bool   `default:"false" usage:"disable DNS proxy"`
		Serve      string `usage:"dns server ip"`
		Serve6     string `usage:"dns server ipv6, eg: ::1"`
		ServeIface string `usage:"use the IP in the net interface, if serve ip not setted. eg: eth0"`
		Upstream   string `usage:"upstream dns server"`
		Fallback   string `default:"223.5.5.5" required:"true" usage:"fallback dns server"`
	}
	Socks5 struct {
		Disable bool   `default:"false" usage:"disable sock5 proxy"`
		Addr    string `default:"127.0.0.1:1080" usage:"socks5 listen address"`
	} `flag:"socks5"`

	Router struct {
		Block struct {
			File       string   `usage:"block list file, local file or remote"`
			FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
			Rules      []string `usage:"block list rules"`
		}
		Direct struct {
			File       string   `usage:"direct list file, local file or remote"`
			FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
			Rules      []string `usage:"direct list rules"`
		}
		Proxy struct {
			File       string   `usage:"proxy list file, local file or remote"`
			FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
			Rules      []string `usage:"proxy list rules"`
		}

		Country struct {
			MMDB       string   `usage:"mmdb file"`
			File       string   `usage:"CIDR block list file, local file or remote"`
			FilePrefix string   `default:"" usage:"parsed as '<prefix>line_text'"`
			Rules      []string `usage:"CIDR list rules"`
		}
	}
}

// Validate implements the validation interface for SowerConfig
func (c *SowerConfig) Validate() error {
	switch c.Remote.Type {
	case "sower", "trojan", "socks5":
	default:
		return fmt.Errorf("unsupported remote type %q", c.Remote.Type)
	}

	remoteHost, err := validateRemoteAddr(c.Remote.Type, c.Remote.Addr)
	if err != nil {
		return err
	}
	if c.Remote.TLS.ClientHello != "" {
		if err := upstreamtls.ValidateClientHello(c.Remote.TLS.ClientHello); err != nil {
			return err
		}
	}

	if c.DNS.ServeIface != "" {
		iface, err := net.InterfaceByName(c.DNS.ServeIface)
		if err != nil {
			return fmt.Errorf("get interface: %w", err)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return fmt.Errorf("get interface addresses: %w", err)
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				return fmt.Errorf("parse interface IP: %w", err)
			}

			if ip.To4() != nil { // ipv4
				if c.DNS.Serve == "" {
					c.DNS.Serve = ip.String()
				}
			} else if ip.IsGlobalUnicast() { // ipv6 must be global unicast
				if c.DNS.Serve6 == "" {
					c.DNS.Serve6 = ip.String()
				}
			}
		}
	}

	if err := validateOptionalIP("dns serve", c.DNS.Serve); err != nil {
		return err
	}
	if err := validateOptionalIP("dns serve6", c.DNS.Serve6); err != nil {
		return err
	}
	if err := validateOptionalIP("dns upstream", c.DNS.Upstream); err != nil {
		return err
	}
	if err := validateRequiredIP("dns fallback", c.DNS.Fallback); err != nil {
		return err
	}

	if !c.DNS.Disable && c.DNS.Serve == "" {
		return fmt.Errorf("dns serve ip and serve interface not set")
	}
	if !c.Socks5.Disable {
		if _, _, err := net.SplitHostPort(c.Socks5.Addr); err != nil {
			return fmt.Errorf("invalid socks5 listen address %q: %w", c.Socks5.Addr, err)
		}
	}

	c.Router.Direct.Rules = append(c.Router.Direct.Rules,
		remoteHost, "**.in-addr.arpa", "**.ip6.arpa")

	return nil
}

func validateRemoteAddr(remoteType, addr string) (string, error) {
	switch remoteType {
	case "socks5":
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return "", fmt.Errorf("invalid remote socks5 address %q: %w", addr, err)
		}
		if host == "" {
			return "", fmt.Errorf("invalid remote socks5 address %q", addr)
		}
		return host, nil
	default:
		host, _, err := splitRemoteAddr(addr, "443")
		if err != nil {
			return "", fmt.Errorf("invalid remote tls address %q: %w", addr, err)
		}
		return host, nil
	}
}

func splitRemoteAddr(addr, defaultPort string) (string, string, error) {
	if addr == "" {
		return "", "", fmt.Errorf("empty address")
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || port == "" {
			return "", "", fmt.Errorf("invalid address %q", addr)
		}
		return host, port, nil
	}

	if strings.HasPrefix(addr, "[") || strings.HasSuffix(addr, "]") {
		return "", "", fmt.Errorf("invalid address %q", addr)
	}
	if strings.Count(addr, ":") == 1 {
		return "", "", fmt.Errorf("missing or invalid port in %q", addr)
	}
	return addr, defaultPort, nil
}

func validateOptionalIP(name, value string) error {
	if value == "" {
		return nil
	}
	return validateRequiredIP(name, value)
}

func validateRequiredIP(name, value string) error {
	if net.ParseIP(value) == nil {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}
