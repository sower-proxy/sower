package config

import (
	"log/slog"
)

// SowerdConfig represents the configuration for sowerd daemon
type SowerdConfig struct {
	LogLevel slog.Level `default:"info" usage:"log level: debug, info, warn, error"`
	ServeIP  string     `required:"true" usage:"listen to port 80 443 of the IP"`
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
	// TODO: Add validation logic if needed
	return nil
}
