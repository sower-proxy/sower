package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
)

func TestLoadExampleSowerConfigFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{name: "toml", file: "sower.toml"},
		{name: "yaml", file: "sower.yaml"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cfg SowerConfig
			if err := loadConfigFileForTest(tt.file, &cfg); err != nil {
				t.Fatalf("load config file: %v", err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate config: %v", err)
			}
			if cfg.Remote.TLS.ServerName != "" {
				t.Fatalf("unexpected remote tls server name: %q", cfg.Remote.TLS.ServerName)
			}
			if cfg.DNS.Serve != "127.0.0.1" {
				t.Fatalf("unexpected dns serve: %q", cfg.DNS.Serve)
			}
			if cfg.DNS.ServeIface != "" {
				t.Fatalf("unexpected dns serve iface: %q", cfg.DNS.ServeIface)
			}
			if cfg.Socks5.Addr != "127.0.0.1:1080" {
				t.Fatalf("unexpected socks5 addr: %q", cfg.Socks5.Addr)
			}
			if cfg.Router.Block.FilePrefix != "**." {
				t.Fatalf("unexpected block file prefix: %q", cfg.Router.Block.FilePrefix)
			}
		})
	}
}

func TestLoadExampleSowerdConfigFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		ext     string
	}{
		{
			name: "toml",
			content: strings.ReplaceAll(
				ExampleSowerdConfigTOML,
				`fake_site = "/var/www"            # Fake site directory or address (default: /var/www)`,
				`fake_site = "127.0.0.1:80"       # Fake site address for tests`,
			),
			ext: ".toml",
		},
		{
			name: "yaml",
			content: strings.ReplaceAll(
				mustReadFileForTest(t, "sowerd.yaml"),
				`fake_site: "/var/www" # Fake site directory or address (default: /var/www)`,
				`fake_site: "127.0.0.1:80" # Fake site address for tests`,
			),
			ext: ".yaml",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "sowerd"+tt.ext)
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write temp config: %v", err)
			}

			var cfg SowerdConfig
			if err := loadConfigFileForTest(path, &cfg); err != nil {
				t.Fatalf("load config file: %v", err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate config: %v", err)
			}
			if cfg.ServeIP != "0.0.0.0" {
				t.Fatalf("unexpected serve ip: %q", cfg.ServeIP)
			}
			if cfg.FakeSite != "127.0.0.1:80" {
				t.Fatalf("unexpected fake site: %q", cfg.FakeSite)
			}
		})
	}
}

func loadConfigFileForTest(path string, dst any) error {
	return aconfig.LoaderFor(dst, aconfig.Config{
		SkipEnv:            true,
		SkipFlags:          true,
		AllowUnknownFields: false,
		Files:              []string{path},
		FileDecoders: map[string]aconfig.FileDecoder{
			".yaml": aconfigyaml.New(),
			".toml": aconfigtoml.New(),
		},
	}).Load()
}

func mustReadFileForTest(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(data)
}
