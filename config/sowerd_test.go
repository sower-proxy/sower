package config

import "testing"

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
					Email string
					Cert  string
					Key   string
				}{
					Cert: "/tmp/cert.pem",
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
