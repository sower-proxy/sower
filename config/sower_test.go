package config

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/sower-proxy/deferlog/v2"
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
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.Remote.TLS.ClientHello = "not-supported"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid TLS client hello")
	}
}

// TestSowerConfigValidateAdminAllowsEmptyPassword pins the startup-fallback
// contract: enabling the admin console without a password must not fail
// validation; cmd/sower generates a random password at runtime and prints it
// to the startup log.
func TestSowerConfigValidateAdminAllowsEmptyPassword(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true
	cfg.Admin.Disable = false
	cfg.Admin.Addr = "127.0.0.1:19090"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate admin with empty password: %v", err)
	}
}

func TestSowerConfigValidateAdminRejectsInvalidAddr(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true
	cfg.Admin.Disable = false
	cfg.Admin.Addr = "127.0.0.1"
	cfg.Admin.Password = deferlog.NewPassword("secret")

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid admin address")
	}
}

func TestSowerConfigValidateAdminAllowsValidConfig(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Type = "sower"
	cfg.Remote.Addr = "example.com"
	cfg.DNS.Disable = true
	cfg.DNS.Fallback = "223.5.5.5"
	cfg.Socks5.Disable = true
	cfg.Admin.Disable = false
	cfg.Admin.Addr = "127.0.0.1:19090"
	cfg.Admin.Password = deferlog.NewPassword("secret")

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
}

// TestSowerConfigAdminDisabledByDefault pins the upgrade-compatible default:
// a config without an [admin] section must load with admin disabled (the aconfig
// default) and pass validation, so existing deployments keep working after
// upgrading without adding an [admin] block or password.
func TestSowerConfigAdminDisabledByDefault(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/sower.toml"
	if err := os.WriteFile(path, []byte(`
[remote]
type = "sower"
addr = "example.com"

[dns]
serve = "127.0.0.1"
fallback = "223.5.5.5"

[socks_5]
disable = true
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
	if !cfg.Admin.Disable {
		t.Fatal("expected admin disabled by default without an [admin] section")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config without admin section: %v", err)
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

[admin]
cookie_secure = true
disable_session_persistence = true

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
	if !cfg.Admin.CookieSecure {
		t.Fatal("expected admin.cookie_secure to load")
	}
	if !cfg.Admin.DisableSessionPersistence {
		t.Fatal("expected admin.disable_session_persistence to load")
	}
	if cfg.AdminSessionFile() != "" {
		t.Fatalf("AdminSessionFile() = %q, want persistence disabled", cfg.AdminSessionFile())
	}
}

func TestSowerConfigLoadsPackagedExamples(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"../config/sower.toml",
		"../.github/sower.toml",
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

// TestSowerConfigAdminPasswordRedactsOnLog pins the masking contract for the
// admin password field: logging a SowerConfig must never leak it. The admin
// password uses deferlog.Password, which renders as *** through fmt.Stringer
// while keeping the plain value reachable via Value(). Remote.Password is a
// plain string (sent verbatim to the upstream proxy) and is masked at the
// startup log site instead, see cmd/sower/main.go.
func TestSowerConfigAdminPasswordRedactsOnLog(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Remote.Password = "topsecret-remote"
	cfg.Admin.Password = deferlog.NewPassword("topsecret-admin")

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Error("load config", "config", cfg)

	out := buf.String()
	for _, secret := range []string{"topsecret-admin"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked in slog output", secret)
		}
	}
	if !strings.Contains(out, "***") {
		t.Fatal("expected masked *** in slog output")
	}
}

// TestSowerConfigAdminPasswordRedactsOnJSON pins the masking contract under a
// JSON handler too: deferlog.Password serializes as base64 via MarshalJSON
// (obfuscation, not plaintext), so the plaintext value must never appear.
// This guards against a future handler change silently degrading the redaction.
func TestSowerConfigAdminPasswordRedactsOnJSON(t *testing.T) {
	t.Parallel()

	cfg := SowerConfig{}
	cfg.Admin.Password = deferlog.NewPassword("topsecret-admin")

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Error("load config", "config", cfg)

	if strings.Contains(buf.String(), "topsecret-admin") {
		t.Fatalf("admin password leaked in slog output: %s", buf.String())
	}
}

// TestSowerConfigRemotePasswordVerbatim pins that the remote password is
// taken verbatim from TOML: unlike the admin password, it must round-trip
// byte-for-byte to the upstream proxy, so even values that happen to be valid
// canonical base64 (like "fnhkwfnh") must not be decoded.
func TestSowerConfigRemotePasswordVerbatim(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/sower.toml"
	if err := os.WriteFile(path, []byte(`
[remote]
type = "sower"
addr = "example.com"
password = "fnhkwfnh"

[dns]
disable = true
fallback = "223.5.5.5"

[socks_5]
disable = true
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
	if got := cfg.Remote.Password; got != "fnhkwfnh" {
		t.Fatalf("remote password = %q, want verbatim fnhkwfnh", got)
	}
}

// TestSowerConfigAdminPasswordAcceptsTaggedBase64 pins that the admin
// password field decodes the explicit "b64:" form from TOML (a
// deferlog.Password feature), so operators can avoid writing plaintext admin
// secrets into config files. Plain values are always verbatim.
func TestSowerConfigAdminPasswordAcceptsTaggedBase64(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/sower.toml"
	if err := os.WriteFile(path, []byte(`
[remote]
type = "sower"
addr = "example.com"

[admin]
disable = false
addr = "127.0.0.1:19090"
password = "b64:c2VjcmV0LXBhc3M="

[dns]
disable = true
fallback = "223.5.5.5"

[socks_5]
disable = true
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
	if got := cfg.Admin.Password.Value(); got != "secret-pass" {
		t.Fatalf("admin tagged base64 password = %q, want secret-pass", got)
	}
}
