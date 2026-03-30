package main

import (
	"log/slog"
	"testing"

	"github.com/sower-proxy/sower/config"
)

func TestSanitizeConfig(t *testing.T) {
	t.Parallel()

	cfg := config.SowerdConfig{
		LogLevel: slog.LevelDebug,
		ServeIP:  "0.0.0.0",
		Password: "secret",
		FakeSite: "127.0.0.1:8080",
	}
	cfg.Cert.Email = "ops@example.com"
	cfg.Cert.Cert = "/tmp/cert.pem"
	cfg.Cert.Key = "/tmp/key.pem"

	got := sanitizeConfig(cfg)
	if got["fake_site"] != cfg.FakeSite {
		t.Fatalf("unexpected fake_site: %#v", got["fake_site"])
	}
	if _, ok := got["password"]; ok {
		t.Fatal("password must not be logged")
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:1234", want: true},
		{addr: "[::1]:443", want: true},
		{addr: "10.0.0.1:1234", want: false},
		{addr: "not-an-addr", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := isLoopbackRemoteAddr(tt.addr); got != tt.want {
				t.Fatalf("isLoopbackRemoteAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHasInstallFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short flag", args: []string{"-i"}, want: true},
		{name: "long flag", args: []string{"--install"}, want: true},
		{name: "missing flag", args: []string{"-c", "/etc/sower/sowerd.toml"}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasInstallFlag(tt.args); got != tt.want {
				t.Fatalf("hasInstallFlag(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
