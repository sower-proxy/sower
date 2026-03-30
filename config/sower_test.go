package config

import "testing"

func TestSowerConfigValidateRejectsInvalidRemoteType(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "invalid"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid remote type")
	}
}

func TestSowerConfigValidateRejectsInvalidSocks5Addr(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true
	cfg.Socks5.Addr = "127.0.0.1"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid socks5 address")
	}
}

func TestSowerConfigValidateRejectsInvalidFallbackIP(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "not-an-ip"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid fallback IP")
	}
}

func TestSowerConfigValidateAllowsTLSRemoteWithExplicitPort(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com:8443"
	cfg.Remote.TLS.ClientHello = "chrome"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	if got := cfg.Router.Direct.Rules[len(cfg.Router.Direct.Rules)-3]; got != "example.com" {
		t.Fatalf("unexpected direct rule host: %q", got)
	}
}

func TestSowerConfigValidateRejectsInvalidTLSClientHello(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "trojan"
	cfg.Remote.Addr = "example.com"
	cfg.Remote.TLS.ClientHello = "not-supported"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid TLS client hello")
	}
}
