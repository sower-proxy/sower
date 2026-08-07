package main

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

// adminConfig adapts the effective sower configuration to the admin
// ConfigManager interface. It keeps the pre-override file/flag config so a
// cleared override can revert the runtime to it, and applies whitelisted
// changes to the log level and the router's DNS upstreams.
type adminConfig struct {
	mu        sync.Mutex // guards effective
	base      config.SowerConfig
	effective config.SowerConfig
	state     *admin.StateStore
	router    *router.Router
}

// newAdminConfig builds the adapter. base must be the configuration before
// admin-state overrides were applied.
func newAdminConfig(base config.SowerConfig, state *admin.StateStore, r *router.Router) *adminConfig {
	effective := base
	applyConfigOverrides(&effective, state.ConfigOverrides())
	return &adminConfig{base: base, effective: effective, state: state, router: r}
}

// ApplyConfigChanges persists the overrides first, then applies them to the
// runtime. A persist failure leaves the runtime untouched.
func (ac *adminConfig) ApplyConfigChanges(changes admin.ConfigChanges, revision uint64) (admin.ConfigView, error) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	overrides := ac.state.ConfigOverrides()
	if changes.LogLevel != nil {
		overrides.LogLevel = strings.ToLower(*changes.LogLevel)
	}
	if changes.DNSUpstream != nil {
		overrides.DNSUpstream = *changes.DNSUpstream
	}
	if changes.DNSFallback != nil {
		overrides.DNSFallback = *changes.DNSFallback
	}

	newRevision, err := ac.state.ApplyConfig(overrides, revision)
	if err != nil {
		return admin.ConfigView{}, err
	}

	ac.effective = ac.base
	applyConfigOverrides(&ac.effective, overrides)
	if changes.DNSUpstream != nil || changes.DNSFallback != nil {
		ac.router.SetDNS(ac.effective.DNS.Upstream, ac.effective.DNS.Fallback)
	}
	// Never log override values: the whitelisted fields are not secrets
	// today, but the revision is all an operator needs to correlate.
	slog.Info("applied admin config override", "revision", newRevision)
	return ac.configViewLocked(), nil
}

func (ac *adminConfig) ConfigView() admin.ConfigView {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.configViewLocked()
}

func (ac *adminConfig) configViewLocked() admin.ConfigView {
	cfg := ac.effective
	overrides := ac.state.ConfigOverrides()
	source := func(overridden bool) string {
		if overridden {
			return admin.SourceOverride
		}
		return admin.SourceConfig
	}

	return admin.ConfigView{
		Revision: ac.state.Revision(),
		Sections: []admin.ConfigSection{
			{Name: "运行", Fields: []admin.ConfigField{
				{Key: "version", Value: version, ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig},
				{Key: "build_date", Value: date, ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig},
				{Key: "log_level", Value: strings.ToLower(cfg.LogLevel.String()), Editable: true,
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.LogLevel != ""),
					Constraint: "debug | info | warn | error"},
			}},
			{Name: "远程代理", Fields: []admin.ConfigField{
				{Key: "remote.type", Value: cfg.Remote.Type, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "remote.addr", Value: cfg.Remote.Addr, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "remote.password", ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig,
					Secret: true, Configured: cfg.Remote.Password != ""},
				{Key: "remote.tls.server_name", Value: cfg.Remote.TLS.ServerName, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "remote.tls.client_hello", Value: cfg.Remote.TLS.ClientHello, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "remote.tls.insecure_skip_verify", Value: strconv.FormatBool(cfg.Remote.TLS.InsecureSkipVerify),
					ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
			}},
			{Name: "DNS", Fields: []admin.ConfigField{
				{Key: "dns.serve", Value: cfg.DNS.Serve, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "dns.serve6", Value: cfg.DNS.Serve6, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "dns.upstream", Value: cfg.DNS.Upstream, Editable: true,
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.DNSUpstream != ""),
					Constraint: "IPv4/IPv6 地址;清空恢复配置文件值"},
				{Key: "dns.fallback", Value: cfg.DNS.Fallback, Editable: true,
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.DNSFallback != ""),
					Constraint: "IPv4/IPv6 地址;清空恢复配置文件值"},
			}},
			{Name: "监听", Fields: []admin.ConfigField{
				{Key: "socks5.addr", Value: cfg.Socks5.Addr, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "admin.addr", Value: cfg.Admin.Addr, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "admin.password", ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig,
					Secret: true, Configured: cfg.Admin.Password != ""},
				{Key: "admin.session_file", Value: cfg.Admin.SessionFile, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "admin.state_file", Value: cfg.Admin.StateFile, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
			}},
			{Name: "规则来源", Fields: ruleSourceFields(cfg)},
		},
	}
}

// ruleSourceFields renders the per-category rule file settings. List values
// collapse to counts; the rules page shows the effective rules themselves.
func ruleSourceFields(cfg config.SowerConfig) []admin.ConfigField {
	type ruleSource struct {
		file   string
		prefix string
		skip   []string
		inline []string
	}
	categories := map[string]ruleSource{
		"block":  {cfg.Router.Block.File, cfg.Router.Block.FilePrefix, cfg.Router.Block.FileSkipRules, cfg.Router.Block.Rules},
		"direct": {cfg.Router.Direct.File, cfg.Router.Direct.FilePrefix, cfg.Router.Direct.FileSkipRules, cfg.Router.Direct.Rules},
		"proxy":  {cfg.Router.Proxy.File, cfg.Router.Proxy.FilePrefix, cfg.Router.Proxy.FileSkipRules, cfg.Router.Proxy.Rules},
	}
	fields := make([]admin.ConfigField, 0, 3*4+3)
	for _, cat := range []string{"block", "direct", "proxy"} {
		rs := categories[cat]
		prefix := "router." + cat + "."
		fields = append(fields,
			admin.ConfigField{Key: prefix + "file", Value: rs.file, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
			admin.ConfigField{Key: prefix + "file_prefix", Value: rs.prefix, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
			admin.ConfigField{Key: prefix + "file_skip_rules", Value: strconv.Itoa(len(rs.skip)) + " 条",
				ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
			admin.ConfigField{Key: prefix + "rules", Value: strconv.Itoa(len(rs.inline)) + " 条",
				ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
		)
	}
	fields = append(fields,
		admin.ConfigField{Key: "router.country.mmdb", Value: cfg.Router.Country.MMDB, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
		admin.ConfigField{Key: "router.country.file", Value: cfg.Router.Country.File, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
		admin.ConfigField{Key: "router.country.rules", Value: strconv.Itoa(len(cfg.Router.Country.Rules)) + " 条",
			ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
	)
	return fields
}
