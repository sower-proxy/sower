package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
)

func TestSowerdConfigPasswordVerbatim(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/sowerd.toml"
	if err := os.WriteFile(path, []byte(`
password = "fnhkwfnh"
fake_site = "127.0.0.1:18080"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var cfg SowerdConfig
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
	// "fnhkwfnh" is valid canonical base64; it must stay verbatim or client
	// auth (byte-for-byte checksum over the password) silently breaks.
	if got := cfg.Password; got != "fnhkwfnh" {
		t.Fatalf("sowerd password = %q, want verbatim fnhkwfnh", got)
	}
}

func TestSowerdConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     SowerdConfig
		wantErr bool
	}{
		{
			name: "valid remote fake site",
			cfg: SowerdConfig{
				ServeIP:  "0.0.0.0",
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
			},
		},
		{
			name: "invalid serve ip",
			cfg: SowerdConfig{
				ServeIP:  "bad-ip",
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "missing password",
			cfg: SowerdConfig{
				FakeSite: "127.0.0.1:8080",
			},
			wantErr: true,
		},
		{
			name: "invalid fake site address",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "not-an-address",
			},
			wantErr: true,
		},
		{
			name: "partial cert config",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				Cert: struct {
					Email   string
					Cert    string
					Key     string
					Domains []string
				}{
					Cert: "/tmp/cert.pem",
				},
			},
			wantErr: true,
		},
		{
			name: "valid site routes",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com", "b.example.com"}, Upstream: "http://127.0.0.1:9000"},
					{Domains: []string{"c.example.com"}, Upstream: "https://backend.example.com"},
				},
			},
		},
		{
			name: "valid path routes",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{
						Domains:  []string{"a.example.com"},
						Upstream: "http://127.0.0.1:9000",
						Routes: map[string]string{
							"/ws":     "http://127.0.0.1:9002",
							"/admin/": "http://127.0.0.1:9003",
						},
					},
				},
			},
		},
		{
			name: "path route without leading slash",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{
						Domains:  []string{"a.example.com"},
						Upstream: "http://127.0.0.1:9000",
						Routes:   map[string]string{"ws": "http://127.0.0.1:9002"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "path route with query",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{
						Domains:  []string{"a.example.com"},
						Upstream: "http://127.0.0.1:9000",
						Routes:   map[string]string{"/ws?x=1": "http://127.0.0.1:9002"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "path route invalid upstream scheme",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{
						Domains:  []string{"a.example.com"},
						Upstream: "http://127.0.0.1:9000",
						Routes:   map[string]string{"/ws": "ftp://127.0.0.1:21"},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty domains",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty upstream",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com"}, Upstream: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid upstream scheme",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com"}, Upstream: "ftp://127.0.0.1:21"},
				},
			},
			wantErr: true,
		},
		{
			name: "upstream missing host",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com"}, Upstream: "http://"},
				},
			},
			wantErr: true,
		},
		{
			name: "wildcard domain rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"*.example.com"}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate domain across routes",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com"}, Upstream: "http://127.0.0.1:9000"},
					{Domains: []string{"a.example.com"}, Upstream: "http://127.0.0.1:9001"},
				},
			},
			wantErr: true,
		},
		{
			name: "empty domain in list",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com", ""}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "domain with whitespace rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{" a.example.com"}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "domain as url rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"https://a.example.com"}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "domain with port rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com:443"}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "domain with trailing dot rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				SiteRoutes: []SiteRoute{
					{Domains: []string{"a.example.com."}, Upstream: "http://127.0.0.1:9000"},
				},
			},
			wantErr: true,
		},
		{
			name: "no site routes is valid",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
			},
		},
		{
			name: "valid cert domains",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				Cert: struct {
					Email   string
					Cert    string
					Key     string
					Domains []string
				}{
					Domains: []string{"proxy.example.com"},
				},
			},
		},
		{
			name: "invalid cert domain rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				Cert: struct {
					Email   string
					Cert    string
					Key     string
					Domains []string
				}{
					Domains: []string{"not a domain"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate cert domain rejected",
			cfg: SowerdConfig{
				Password: "secret",
				FakeSite: "127.0.0.1:8080",
				Cert: struct {
					Email   string
					Cert    string
					Key     string
					Domains []string
				}{
					Domains: []string{"proxy.example.com", "proxy.example.com"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSowerdConfigValidateSiteRouteCertificate(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeTestCertificate(t, []string{"a.example.com", "b.example.com"})

	cfg := SowerdConfig{
		Password: "secret",
		FakeSite: "127.0.0.1:8080",
		SiteRoutes: []SiteRoute{
			{Domains: []string{"a.example.com", "b.example.com"}, Upstream: "http://127.0.0.1:9000"},
		},
	}
	cfg.Cert.Cert = certPath
	cfg.Cert.Key = keyPath
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	cfg.SiteRoutes = []SiteRoute{
		{Domains: []string{"missing.example.com"}, Upstream: "http://127.0.0.1:9000"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for uncovered site route domain")
	}
}

func writeTestCertificate(t *testing.T, domains []string) (string, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: domains[0],
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		DNSNames:  domains,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	certFile, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("write cert file: %v", err)
	}
	if err := certFile.Close(); err != nil {
		t.Fatalf("close cert file: %v", err)
	}

	keyFile, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := keyFile.Close(); err != nil {
		t.Fatalf("close key file: %v", err)
	}

	return certPath, keyPath
}
