package config

import (
	"os"
	"testing"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
)

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

func TestSowerConfigLoadsTOMLFileSkipRules(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/sower.toml"
	if err := os.WriteFile(path, []byte(`
[remote]
type = "sower"
addr = "example.com"

[dns]
disable = true
fallback = "223.5.5.5"

[socks_5]
disable = true

[router.block]
file_skip_rules = ["t.co"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var cfg SowerConfig
	if err := aconfig.LoaderFor(&cfg, aconfig.Config{
		SkipEnv:   true,
		SkipFlags: true,
		Files:     []string{path},
		FileDecoders: map[string]aconfig.FileDecoder{
			".toml": aconfigtoml.New(),
		},
	}).Load(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Router.Block.FileSkipRules) != 1 || cfg.Router.Block.FileSkipRules[0] != "t.co" {
		t.Fatalf("unexpected file skip rules: %v", cfg.Router.Block.FileSkipRules)
	}
	if !cfg.Socks5.Disable {
		t.Fatal("expected socks_5 section to load")
	}
}

func TestSowerConfigLoadsPackagedExamples(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"../config/sower.toml",
		"../config/sower.yaml",
		"../.github/sower.toml",
		"../.github/sower.yaml",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			var cfg SowerConfig
			if err := aconfig.LoaderFor(&cfg, aconfig.Config{
				SkipEnv:   true,
				SkipFlags: true,
				Files:     []string{path},
				FileDecoders: map[string]aconfig.FileDecoder{
					".toml": aconfigtoml.New(),
					".yaml": aconfigyaml.New(),
				},
			}).Load(); err != nil {
				t.Fatalf("load config: %v", err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate config: %v", err)
			}
		})
	}
}
