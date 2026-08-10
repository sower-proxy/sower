package main

import (
	"log/slog"
	"slices"
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
// changes to the log level and the router's DNS upstreams immediately;
// every other whitelisted field takes effect on the next restart.
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
	// Merge semantics: only fields present in changes are touched; absent
	// fields keep their previous override. Within a present field, an empty
	// value clears the override so the file/flag configuration takes over
	// again, and a non-empty value sets the override.
	applyStr := func(dst **string, v *string) {
		if v == nil {
			return
		}
		if *v == "" {
			*dst = nil
			return
		}
		s := *v
		*dst = &s
	}
	applyList := func(dst **[]string, v *[]string) {
		if v == nil {
			return
		}
		if len(*v) == 0 {
			*dst = nil
			return
		}
		c := slices.Clone(*v)
		*dst = &c
	}
	if changes.LogLevel != nil {
		lv := strings.ToLower(*changes.LogLevel)
		applyStr(&overrides.LogLevel, &lv)
	}
	applyStr(&overrides.DNSUpstream, changes.DNSUpstream)
	applyStr(&overrides.DNSFallback, changes.DNSFallback)
	applyStr(&overrides.RemoteType, changes.RemoteType)
	applyStr(&overrides.RemoteAddr, changes.RemoteAddr)
	applyStr(&overrides.RemoteTLSServerName, changes.RemoteTLSServerName)
	applyStr(&overrides.RemoteTLSClientHello, changes.RemoteTLSClientHello)
	applyStr(&overrides.RemoteTLSInsecureSkipVerify, changes.RemoteTLSInsecureSkipVerify)
	applyStr(&overrides.DNSServe, changes.DNSServe)
	applyStr(&overrides.DNSServe6, changes.DNSServe6)
	applyStr(&overrides.Socks5Addr, changes.Socks5Addr)
	applyStr(&overrides.AdminSessionFile, changes.AdminSessionFile)
	applyStr(&overrides.AdminDisableSessionPersistence, changes.AdminDisableSessionPersistence)
	applyStr(&overrides.AdminCookieSecure, changes.AdminCookieSecure)
	applyStr(&overrides.AdminStateFile, changes.AdminStateFile)
	applyStr(&overrides.RouterBlockFile, changes.RouterBlockFile)
	applyStr(&overrides.RouterBlockFilePrefix, changes.RouterBlockFilePrefix)
	applyList(&overrides.RouterBlockFileSkipRules, changes.RouterBlockFileSkipRules)
	applyList(&overrides.RouterBlockRules, changes.RouterBlockRules)
	applyStr(&overrides.RouterDirectFile, changes.RouterDirectFile)
	applyStr(&overrides.RouterDirectFilePrefix, changes.RouterDirectFilePrefix)
	applyList(&overrides.RouterDirectFileSkipRules, changes.RouterDirectFileSkipRules)
	applyList(&overrides.RouterDirectRules, changes.RouterDirectRules)
	applyStr(&overrides.RouterProxyFile, changes.RouterProxyFile)
	applyStr(&overrides.RouterProxyFilePrefix, changes.RouterProxyFilePrefix)
	applyList(&overrides.RouterProxyFileSkipRules, changes.RouterProxyFileSkipRules)
	applyList(&overrides.RouterProxyRules, changes.RouterProxyRules)
	applyStr(&overrides.RouterCountryMMDB, changes.RouterCountryMMDB)
	applyStr(&overrides.RouterCountryFile, changes.RouterCountryFile)
	applyList(&overrides.RouterCountryRules, changes.RouterCountryRules)

	newRevision, err := ac.state.ApplyConfig(overrides, revision)
	if err != nil {
		return admin.ConfigView{}, err
	}

	ac.effective = ac.base
	applyConfigOverrides(&ac.effective, overrides)
	// Immediate-effect fields apply to the running process; everything else
	// takes effect on the next restart.
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
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.LogLevel != nil),
					Constraint: "debug | info | warn | error",
					Type:       "enum", Options: []string{"debug", "info", "warn", "error"}},
			}},
			{Name: "远程代理", Fields: []admin.ConfigField{
				{Key: "remote.type", Value: cfg.Remote.Type, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.RemoteType != nil),
					Constraint: "sower | socks5"},
				{Key: "remote.addr", Value: cfg.Remote.Addr, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.RemoteAddr != nil),
					Constraint: "代理地址，如 proxy.com 或 proxy.com:443"},
				{Key: "remote.password", ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig,
					Secret: true, Configured: cfg.Remote.Password.Value() != ""},
				{Key: "remote.tls.server_name", Value: cfg.Remote.TLS.ServerName, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.RemoteTLSServerName != nil),
					Constraint: "覆盖上游 TLS SNI；留空使用地址"},
				{Key: "remote.tls.client_hello", Value: cfg.Remote.TLS.ClientHello, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.RemoteTLSClientHello != nil),
					Constraint: "chrome | firefox | ios | android | edge | safari | randomized | golang"},
				{Key: "remote.tls.insecure_skip_verify", Value: strconv.FormatBool(cfg.Remote.TLS.InsecureSkipVerify),
					Editable: true, ApplyMode: admin.ApplyRestart, Source: source(overrides.RemoteTLSInsecureSkipVerify != nil),
					Type: "bool"},
			}},
			{Name: "DNS", Fields: []admin.ConfigField{
				{Key: "dns.serve", Value: cfg.DNS.Serve, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.DNSServe != nil),
					Constraint: "IPv4 监听地址；留空自动探测"},
				{Key: "dns.serve6", Value: cfg.DNS.Serve6, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.DNSServe6 != nil),
					Constraint: "IPv6 监听地址，如 ::1"},
				{Key: "dns.upstream", Value: cfg.DNS.Upstream, Editable: true,
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.DNSUpstream != nil),
					Constraint: "IPv4/IPv6 地址；清空恢复配置文件值"},
				{Key: "dns.fallback", Value: cfg.DNS.Fallback, Editable: true,
					ApplyMode: admin.ApplyImmediate, Source: source(overrides.DNSFallback != nil),
					Constraint: "IPv4/IPv6 地址；清空恢复配置文件值"},
			}},
			{Name: "监听", Fields: []admin.ConfigField{
				{Key: "socks5.addr", Value: cfg.Socks5.Addr, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.Socks5Addr != nil),
					Constraint: "SOCKS5 监听地址，如 127.0.0.1:1080"},
				{Key: "admin.addr", Value: cfg.Admin.Addr, ApplyMode: admin.ApplyRestart, Source: admin.SourceConfig},
				{Key: "admin.password", ApplyMode: admin.ApplyReadonly, Source: admin.SourceConfig,
					Secret: true, Configured: cfg.Admin.Password.Value() != ""},
				{Key: "admin.session_file", Value: cfg.Admin.SessionFile, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.AdminSessionFile != nil),
					Constraint: "会话持久化文件路径；留空不持久化"},
				{Key: "admin.disable_session_persistence", Value: strconv.FormatBool(cfg.Admin.DisableSessionPersistence),
					Editable: true, ApplyMode: admin.ApplyRestart, Source: source(overrides.AdminDisableSessionPersistence != nil),
					Type: "bool"},
				{Key: "admin.cookie_secure", Value: strconv.FormatBool(cfg.Admin.CookieSecure),
					Editable: true, ApplyMode: admin.ApplyRestart, Source: source(overrides.AdminCookieSecure != nil),
					Type: "bool"},
				{Key: "admin.state_file", Value: cfg.Admin.StateFile, Editable: true,
					ApplyMode: admin.ApplyRestart, Source: source(overrides.AdminStateFile != nil),
					Constraint: "admin 状态文件路径；留空不持久化"},
			}},
			{Name: "规则来源", Fields: ruleSourceFields(cfg, overrides)},
		},
	}
}

// ruleSourceFields renders the per-category rule file settings. List values
// carry the full inline lists (one entry per line) so the console can edit
// them; the display collapses them to counts.
func ruleSourceFields(cfg config.SowerConfig, o admin.ConfigOverrides) []admin.ConfigField {
	source := func(overridden bool) string {
		if overridden {
			return admin.SourceOverride
		}
		return admin.SourceConfig
	}
	fields := make([]admin.ConfigField, 0, 3*4+3)
	for _, cat := range []string{"block", "direct", "proxy"} {
		var file, prefix string
		var skip, inline []string
		var fileO, prefixO *string
		var skipO, inlineO *[]string
		switch cat {
		case "block":
			file, prefix, skip, inline = cfg.Router.Block.File, cfg.Router.Block.FilePrefix, cfg.Router.Block.FileSkipRules, cfg.Router.Block.Rules
			fileO, prefixO, skipO, inlineO = o.RouterBlockFile, o.RouterBlockFilePrefix, o.RouterBlockFileSkipRules, o.RouterBlockRules
		case "direct":
			file, prefix, skip, inline = cfg.Router.Direct.File, cfg.Router.Direct.FilePrefix, cfg.Router.Direct.FileSkipRules, cfg.Router.Direct.Rules
			fileO, prefixO, skipO, inlineO = o.RouterDirectFile, o.RouterDirectFilePrefix, o.RouterDirectFileSkipRules, o.RouterDirectRules
		case "proxy":
			file, prefix, skip, inline = cfg.Router.Proxy.File, cfg.Router.Proxy.FilePrefix, cfg.Router.Proxy.FileSkipRules, cfg.Router.Proxy.Rules
			fileO, prefixO, skipO, inlineO = o.RouterProxyFile, o.RouterProxyFilePrefix, o.RouterProxyFileSkipRules, o.RouterProxyRules
		}
		p := "router." + cat + "."
		fields = append(fields,
			admin.ConfigField{Key: p + "file", Value: file, Editable: true,
				ApplyMode: admin.ApplyRestart, Source: source(fileO != nil),
				Constraint: "规则文件路径或 URL；留空不加载"},
			admin.ConfigField{Key: p + "file_prefix", Value: prefix, Editable: true,
				ApplyMode: admin.ApplyRestart, Source: source(prefixO != nil),
				Constraint: "文件规则前缀，如 **."},
			admin.ConfigField{Key: p + "file_skip_rules", Value: strings.Join(skip, "\n"), Editable: true,
				Type: "list", ApplyMode: admin.ApplyRestart, Source: source(skipO != nil),
				Constraint: "跳过文件中的规则，每行一条"},
			admin.ConfigField{Key: p + "rules", Value: strings.Join(inline, "\n"), Editable: true,
				Type: "list", ApplyMode: admin.ApplyRestart, Source: source(inlineO != nil),
				Constraint: "内联规则，每行一条"},
		)
	}
	fields = append(fields,
		admin.ConfigField{Key: "router.country.mmdb", Value: cfg.Router.Country.MMDB, Editable: true,
			ApplyMode: admin.ApplyRestart, Source: source(o.RouterCountryMMDB != nil),
			Constraint: "GeoIP MMDB 文件路径；留空不启用"},
		admin.ConfigField{Key: "router.country.file", Value: cfg.Router.Country.File, Editable: true,
			ApplyMode: admin.ApplyRestart, Source: source(o.RouterCountryFile != nil),
			Constraint: "国家网段规则文件路径或 URL"},
		admin.ConfigField{Key: "router.country.rules", Value: strings.Join(cfg.Router.Country.Rules, "\n"), Editable: true,
			Type: "list", ApplyMode: admin.ApplyRestart, Source: source(o.RouterCountryRules != nil),
			Constraint: "内联 CIDR，每行一条"},
	)
	return fields
}
