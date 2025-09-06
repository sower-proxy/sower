package config

import (
	"fmt"
	"log/slog"
	"net"
)

// SowerConfig represents the configuration for sower client
type SowerConfig struct {
	LogLevel slog.Level `default:"info" usage:"log level: debug, info, warn, error"`

	Remote struct {
		Type     string `default:"sower" required:"true" usage:"option: sower/trojan/socks5"`
		Addr     string `required:"true" usage:"proxy address, eg: proxy.com/127.0.0.1:7890"`
		Password string `usage:"remote proxy password"`
	}

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

	if !c.DNS.Disable && c.DNS.Serve == "" {
		return fmt.Errorf("dns serve ip and serve interface not set")
	}

	c.Router.Direct.Rules = append(c.Router.Direct.Rules,
		c.Remote.Addr, "**.in-addr.arpa", "**.ip6.arpa")

	return nil
}
