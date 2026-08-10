package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/lmittmann/tint"
	"github.com/miekg/dns"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/pkg/suffixtree"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
	"github.com/sower-proxy/sower/router"
)

var (
	version, date string
	conf          config.SowerConfig
	// logLevel backs the tint handler so the admin console can adjust the
	// log level at runtime; it defaults to INFO until the config is loaded.
	logLevel slog.LevelVar
)

// defaultMemoryLimitMiB bounds the Go heap unless GOMEMLIMIT or
// SOWER_MEMORY_LIMIT_MB overrides it; see init for the rationale.
const defaultMemoryLimitMiB = 128

// maxMemoryLimitMiB caps the SOWER_MEMORY_LIMIT_MB override so the MiB
// shift cannot overflow int64.
const maxMemoryLimitMiB int64 = 1 << 20 // 1 TiB

func init() {
	fi, _ := os.Stdout.Stat()
	noColor := (fi.Mode() & os.ModeCharDevice) == 0
	deferlog.SetDefault(slog.New(tint.NewHandler(os.Stdout,
		&tint.Options{AddSource: true, NoColor: noColor, Level: &logLevel})))
	if strings.HasSuffix(os.Args[0], ".test") {
		return
	}

	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		AllowUnknownFields: false,
		FileFlag:           "c",
		Files: []string{
			"sower.toml",
			"/etc/sower/sower.toml",
		},
		FileDecoders: map[string]aconfig.FileDecoder{
			".toml": aconfigtoml.New(),
		},
	}).Load(); err != nil {
		slog.Error("load config", "error", err, "config", conf)
		os.Exit(1)
	}

	if err := conf.Validate(); err != nil {
		slog.Error("validate config", "error", err)
		os.Exit(1)
	}

	// Bound the Go heap with a soft memory limit. The default rule sets
	// (adlist/chinalist/gfwlist) build ~40MB of suffix trees, and GOGC's
	// 2x target on top of that pushes resident memory toward 250MB on a
	// gateway that mostly idles. The soft limit makes GC reclaim eagerly
	// and return memory to the OS.
	//
	// Precedence: an explicit standard GOMEMLIMIT wins; otherwise the
	// default 128MiB applies unless SOWER_MEMORY_LIMIT_MB overrides it
	// (a positive MiB value, or 0 to disable the soft limit). Malformed or
	// negative values leave the default in place.
	if os.Getenv("GOMEMLIMIT") == "" {
		var limit int64 = defaultMemoryLimitMiB
		if v := os.Getenv("SOWER_MEMORY_LIMIT_MB"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				switch {
				case n == 0:
					debug.SetMemoryLimit(math.MaxInt64) // disable
				case n > 0 && n <= maxMemoryLimitMiB:
					limit = n
				}
			}
		}
		if limit > 0 {
			debug.SetMemoryLimit(limit << 20)
		}
	}

	logLevel.Set(conf.LogLevel)
	slog.Info("starting sower",
		"version", version,
		"date", date,
		"log_level", conf.LogLevel,
		"remote_type", conf.Remote.Type,
		"remote_addr", conf.Remote.Addr,
		"remote_password", deferlog.Secret(conf.Remote.Password),
		"remote_tls", conf.Remote.TLS,
		"dns", conf.DNS,
		"socks5", conf.Socks5,
		"router", conf.Router)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, stop, conf); err != nil {
		slog.Error("run sower", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stop context.CancelFunc, cfg config.SowerConfig) error {
	stateStore := loadAdminState(cfg)
	baseCfg := cfg
	applyConfigOverrides(&cfg, stateStore.ConfigOverrides())

	upstreamDNS := effectiveUpstreamDNS(cfg)
	proxyDial, err := GenProxyDial(cfg.Remote.Type, cfg.Remote.Addr, cfg.Remote.Password, upstreamDNS, upstreamtls.Options{
		ServerName:         cfg.Remote.TLS.ServerName,
		ClientHello:        cfg.Remote.TLS.ClientHello,
		InsecureSkipVerify: cfg.Remote.TLS.InsecureSkipVerify,
	})
	if err != nil {
		return fmt.Errorf("build proxy dialer: %w", err)
	}
	r, err := newRouter(cfg, proxyDial)
	if err != nil {
		return fmt.Errorf("build router: %w", err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			slog.Warn("close router", "error", err)
		}
	}()

	stats, err := admin.NewStats()
	if err != nil {
		return fmt.Errorf("init stats: %w", err)
	}
	blockHits := newRuleHitTracker(r.BlockRule, maxRuleHits)
	directHits := newRuleHitTracker(r.DirectRule, maxRuleHitsWide)
	proxyHits := newRuleHitTracker(r.ProxyRule, maxRuleHitsWide)
	missHits := newRuleMissTracker()
	r.SetRouteObserver(func(c router.RouteCategory, domain string) {
		stats.RecordRoute(string(c), domain)
	})
	r.SetRuleHitObserver(func(c router.RouteCategory, domain string) {
		switch c {
		case router.RouteBlock:
			blockHits.OnHit(domain)
		case router.RouteDirect:
			directHits.OnHit(domain)
		case router.RouteProxy:
			proxyHits.OnHit(domain)
		}
	})
	r.SetRuleMissObserver(func(domain string) {
		missHits.OnMiss(domain)
	})

	start := time.Now()
	if err := loadRouterRules(ctx, r, proxyDial, cfg); err != nil {
		return err
	}

	baseline := snapshotBaseline(r)
	stateStore.SetBaseline(baseline)
	applyRuleDeltas(r, stateStore)
	rulesMgr := newAdminRules(r, stateStore, baseline, blockHits, directHits, proxyHits, missHits)
	configMgr := newAdminConfig(baseCfg, stateStore, r)

	errCh := make(chan error, 8)
	// restartCh coalesces restart requests from the admin API; the process
	// replaces itself in place (same PID) so systemd stays unaware.
	restartCh := make(chan struct{}, 1)
	var wg sync.WaitGroup
	if err := startDNSListeners(ctx, &wg, cfg, r, stats, errCh); err != nil {
		return err
	}
	if err := startSocks5Listener(ctx, &wg, cfg, r, stats, errCh); err != nil {
		return err
	}
	if _, shared := sharedAdminHTTPAddr(cfg); shared {
		if err := startSharedHTTPListener(ctx, &wg, cfg, r, rulesMgr, configMgr, stats, errCh, restartCh); err != nil {
			return err
		}
	} else if err := startAdminListener(ctx, &wg, cfg, rulesMgr, configMgr, stats, errCh, restartCh); err != nil {
		return err
	}

	slog.Info("loaded rules, proxy started", "took", time.Since(start),
		"blockRule", r.BlockRule.Count(), "directRule", r.DirectRule.Count(), "proxyRule", r.ProxyRule.Count())
	runtime.GC()

	select {
	case <-ctx.Done():
		slog.Info("shutting down sower", "reason", ctx.Err())
	case <-restartCh:
		slog.Info("restarting sower")
		stop()
		// Wait for every listener to close so the replacement process can
		// bind the same addresses (exec keeps the old file descriptors).
		// Bound the wait: a stuck close must not leave a half-alive process.
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			slog.Warn("listener shutdown timed out, restarting anyway")
		}
		if err := restartCurrentProcess(); err != nil {
			return fmt.Errorf("restart current process: %w", err)
		}
	case err := <-errCh:
		slog.Error("serve failed", "error", err)
		stop()
	}
	return nil
}

func effectiveUpstreamDNS(cfg config.SowerConfig) string {
	if cfg.DNS.Upstream != "" {
		return cfg.DNS.Upstream
	}
	return cfg.DNS.Fallback
}

// loadAdminState opens the admin state store. An empty state file path or
// the --ignore-admin-state escape hatch yields an in-memory store, so the
// rest of the code never has to nil-check persistence.
func loadAdminState(cfg config.SowerConfig) *admin.StateStore {
	switch {
	case cfg.Admin.StateFile == "":
		return admin.LoadStateStore("")
	case cfg.IgnoreAdminState:
		slog.Warn("ignoring admin state file", "file", cfg.Admin.StateFile)
		return admin.LoadStateStore("")
	default:
		return admin.LoadStateStore(cfg.Admin.StateFile)
	}
}

// applyConfigOverrides applies the whitelisted admin-state config overrides
// to cfg before the router and dialer are built. Invalid values are dropped
// with a warning so a bad override never blocks startup; the
// --ignore-admin-state flag skips the file entirely.
func applyConfigOverrides(cfg *config.SowerConfig, o admin.ConfigOverrides) {
	// An empty override (nil or empty value) is skipped so the file/flag
	// configuration wins; a non-empty value overrides it. Clearing an
	// override in the admin console therefore reverts to the file/flag
	// configuration on the next restart.
	applyStr := func(dst *string, v *string) {
		if v != nil && *v != "" {
			*dst = *v
		}
	}
	applyList := func(dst *[]string, v *[]string) {
		if v != nil && len(*v) > 0 {
			*dst = slices.Clone(*v)
		}
	}
	applyBool := func(dst *bool, v *string, field string) {
		if v == nil || *v == "" {
			return
		}
		b, err := strconv.ParseBool(*v)
		if err != nil {
			slog.Warn("ignore invalid admin state override", "field", field, "error", err)
			return
		}
		*dst = b
	}

	if o.LogLevel != nil && *o.LogLevel != "" {
		var lv slog.Level
		if err := lv.UnmarshalText([]byte(strings.ToUpper(*o.LogLevel))); err != nil {
			slog.Warn("ignore invalid admin state override", "field", "log_level", "error", err)
		} else {
			cfg.LogLevel = lv
		}
	}
	if o.DNSUpstream != nil && *o.DNSUpstream != "" {
		if net.ParseIP(*o.DNSUpstream) == nil {
			slog.Warn("ignore invalid admin state override", "field", "dns_upstream")
		} else {
			cfg.DNS.Upstream = *o.DNSUpstream
		}
	}
	if o.DNSFallback != nil && *o.DNSFallback != "" {
		if net.ParseIP(*o.DNSFallback) == nil {
			slog.Warn("ignore invalid admin state override", "field", "dns_fallback")
		} else {
			cfg.DNS.Fallback = *o.DNSFallback
		}
	}
	applyStr(&cfg.Remote.Type, o.RemoteType)
	applyStr(&cfg.Remote.Addr, o.RemoteAddr)
	applyStr(&cfg.Remote.TLS.ServerName, o.RemoteTLSServerName)
	applyStr(&cfg.Remote.TLS.ClientHello, o.RemoteTLSClientHello)
	applyBool(&cfg.Remote.TLS.InsecureSkipVerify, o.RemoteTLSInsecureSkipVerify, "remote_tls_insecure_skip_verify")
	applyStr(&cfg.DNS.Serve, o.DNSServe)
	applyStr(&cfg.DNS.Serve6, o.DNSServe6)
	applyStr(&cfg.Socks5.Addr, o.Socks5Addr)
	applyStr(&cfg.Admin.SessionFile, o.AdminSessionFile)
	applyBool(&cfg.Admin.DisableSessionPersistence, o.AdminDisableSessionPersistence, "admin_disable_session_persistence")
	applyBool(&cfg.Admin.CookieSecure, o.AdminCookieSecure, "admin_cookie_secure")
	applyStr(&cfg.Admin.StateFile, o.AdminStateFile)
	applyStr(&cfg.Router.Block.File, o.RouterBlockFile)
	applyStr(&cfg.Router.Block.FilePrefix, o.RouterBlockFilePrefix)
	applyList(&cfg.Router.Block.FileSkipRules, o.RouterBlockFileSkipRules)
	applyList(&cfg.Router.Block.Rules, o.RouterBlockRules)
	applyStr(&cfg.Router.Direct.File, o.RouterDirectFile)
	applyStr(&cfg.Router.Direct.FilePrefix, o.RouterDirectFilePrefix)
	applyList(&cfg.Router.Direct.FileSkipRules, o.RouterDirectFileSkipRules)
	applyList(&cfg.Router.Direct.Rules, o.RouterDirectRules)
	applyStr(&cfg.Router.Proxy.File, o.RouterProxyFile)
	applyStr(&cfg.Router.Proxy.FilePrefix, o.RouterProxyFilePrefix)
	applyList(&cfg.Router.Proxy.FileSkipRules, o.RouterProxyFileSkipRules)
	applyList(&cfg.Router.Proxy.Rules, o.RouterProxyRules)
	applyStr(&cfg.Router.Country.MMDB, o.RouterCountryMMDB)
	applyStr(&cfg.Router.Country.File, o.RouterCountryFile)
	applyList(&cfg.Router.Country.Rules, o.RouterCountryRules)

	logLevel.Set(cfg.LogLevel)
}

// applyRuleDeltas replays persisted admin rule changes onto the freshly
// loaded rule sets: tombstoned baseline rules leave, admin additions enter.
// The rule set is rebuilt via Replace because RuleSet.Remove rebuilds the
// suffix tree per call, which is expensive beyond a handful of tombstones.
func applyRuleDeltas(r *router.Router, state *admin.StateStore) {
	sets := map[admin.Category]*router.RuleSet{
		admin.CategoryBlock:  r.BlockRule,
		admin.CategoryDirect: r.DirectRule,
		admin.CategoryProxy:  r.ProxyRule,
	}
	for cat, rs := range sets {
		d := state.Delta(cat)
		if len(d.Add) == 0 && len(d.Remove) == 0 {
			continue
		}
		tombstoned := make(map[string]struct{}, len(d.Remove))
		for _, rule := range d.Remove {
			tombstoned[rule] = struct{}{}
		}
		rules := rs.List()
		effective := make([]string, 0, len(rules)+len(d.Add))
		for _, rule := range rules {
			if _, ok := tombstoned[rule]; !ok {
				effective = append(effective, rule)
			}
		}
		rs.Replace(append(effective, d.Add...)...)
		slog.Info("applied admin rule deltas", "category", cat, "added", len(d.Add), "removed", len(d.Remove))
	}
}

func newRouter(cfg config.SowerConfig, proxyDial router.ProxyDialFn) (*router.Router, error) {
	r, err := router.NewRouter([]string{cfg.DNS.Serve, cfg.DNS.Serve6}, cfg.DNS.Upstream, cfg.DNS.Fallback, cfg.Router.Country.MMDB, proxyDial)
	if err != nil {
		return nil, err
	}
	r.BlockRule.Add(cfg.Router.Block.Rules...)
	r.DirectRule.Add(cfg.Router.Direct.Rules...)
	r.ProxyRule.Add(cfg.Router.Proxy.Rules...)
	if err := r.AddCountryCIDRs(cfg.Router.Country.Rules...); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func loadRouterRules(ctx context.Context, r *router.Router, proxyDial router.ProxyDialFn, cfg config.SowerConfig) error {
	if err := loadRule(ctx, r.BlockRule, proxyDial, cfg.Router.Block.File, cfg.Router.Block.FilePrefix, cfg.Router.Block.FileSkipRules); err != nil {
		return fmt.Errorf("load block rules: %w", err)
	}
	if err := loadRule(ctx, r.DirectRule, proxyDial, cfg.Router.Direct.File, cfg.Router.Direct.FilePrefix, cfg.Router.Direct.FileSkipRules); err != nil {
		return fmt.Errorf("load direct rules: %w", err)
	}
	if err := loadRule(ctx, r.ProxyRule, proxyDial, cfg.Router.Proxy.File, cfg.Router.Proxy.FilePrefix, cfg.Router.Proxy.FileSkipRules); err != nil {
		return fmt.Errorf("load proxy rules: %w", err)
	}
	countryLines, err := fetchRuleFile(ctx, proxyDial, cfg.Router.Country.File)
	if err != nil {
		return fmt.Errorf("load country rules: %w", err)
	}
	if err := r.AddCountryCIDRs(countryLines...); err != nil {
		return fmt.Errorf("load country rules: %w", err)
	}
	return nil
}

func startDNSListeners(ctx context.Context, wg *sync.WaitGroup, cfg config.SowerConfig, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	if cfg.DNS.Disable {
		slog.Info("DNS proxy disabled")
		return nil
	}

	_, shared := sharedAdminHTTPAddr(cfg)
	for _, ip := range dnsListenIPs(cfg) {
		// In shared mode the admin console takes over the primary HTTP
		// listener; the HTTPS and DNS listeners still start normally.
		if !(shared && ip == cfg.DNS.Serve) {
			if err := startHTTPListener(ctx, wg, ip, r, stats, errCh); err != nil {
				return err
			}
		}
		if err := startHTTPSListener(ctx, wg, ip, r, stats, errCh); err != nil {
			return err
		}
		if err := startDNSUDPListener(ctx, wg, ip, r, stats, errCh); err != nil {
			return err
		}
	}
	return nil
}

func dnsListenIPs(cfg config.SowerConfig) []string {
	ips := make([]string, 0, 2)
	if strings.TrimSpace(cfg.DNS.Serve) != "" {
		ips = append(ips, cfg.DNS.Serve)
	}
	if strings.TrimSpace(cfg.DNS.Serve6) != "" {
		ips = append(ips, cfg.DNS.Serve6)
	}
	return ips
}

func startHTTPListener(ctx context.Context, wg *sync.WaitGroup, ip string, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "80")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen http proxy on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "http proxy", "network", "tcp", "addr", addr)
	wg.Add(1)
	go closeOnDone(ctx, wg, ln)
	go serveAndReport(errCh, "http proxy", func() error {
		return ServeHTTP(ctx, ln, r, stats)
	})
	return nil
}

func startHTTPSListener(ctx context.Context, wg *sync.WaitGroup, ip string, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "443")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen https proxy on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "https proxy", "network", "tcp", "addr", addr)
	wg.Add(1)
	go closeOnDone(ctx, wg, ln)
	go serveAndReport(errCh, "https proxy", func() error {
		return ServeHTTPS(ctx, ln, r, stats)
	})
	return nil
}

func startDNSUDPListener(ctx context.Context, wg *sync.WaitGroup, ip string, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "53")
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen dns proxy on %s: %w", addr, err)
	}

	server := &dns.Server{
		PacketConn: pc,
		Handler:    dnsStatsHandler{Handler: r, stats: stats},
	}
	slog.Info("service listening", "service", "dns proxy", "network", "udp", "addr", addr)
	wg.Add(1)
	go shutdownDNSServerOnDone(ctx, wg, server)
	go func() {
		if err := server.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			reportServeError(errCh, "dns proxy", fmt.Errorf("serve on %s: %w", addr, err))
		}
	}()
	return nil
}

func startSocks5Listener(ctx context.Context, wg *sync.WaitGroup, cfg config.SowerConfig, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	if cfg.Socks5.Disable {
		slog.Info("SOCKS5 proxy disabled")
		return nil
	}

	ln, err := net.Listen("tcp", cfg.Socks5.Addr)
	if err != nil {
		return fmt.Errorf("listen socks5 proxy on %s: %w", cfg.Socks5.Addr, err)
	}
	slog.Info("service listening", "service", "socks5 proxy", "network", "tcp", "addr", cfg.Socks5.Addr)
	wg.Add(1)
	go closeOnDone(ctx, wg, ln)
	go serveAndReport(errCh, "socks5 proxy", func() error {
		return ServeSocks5(ctx, ln, r, stats)
	})
	return nil
}

func loadRule(ctx context.Context, rule *router.RuleSet, proxyDial router.ProxyDialFn, file, linePrefix string, skipRules []string) error {
	skipRule := suffixtree.NewNodeFromRules(skipRules...)
	lines, err := fetchRuleFile(ctx, proxyDial, file)
	if err != nil {
		return err
	}
	for _, line := range lines {
		item := linePrefix + line
		if skipRule.Match(line) || skipRule.Match(item) {
			continue
		}
		rule.Add(item)
	}
	rule.Compact()
	return nil
}

func fetchRuleFile(ctx context.Context, proxyDial router.ProxyDialFn, file string) ([]string, error) {
	if file == "" {
		return nil, nil
	}

	var loadFn func() (io.ReadCloser, error)
	if _, err := os.Stat(file); err == nil {
		loadFn = func() (io.ReadCloser, error) {
			return os.Open(file)
		}
	} else {
		if proxyDial == nil {
			return nil, fmt.Errorf("remote rule file %q requires upstream proxy dialer", file)
		}
		var lastDialErr error
		client := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					domain, port, _ := net.SplitHostPort(addr)
					p, _ := strconv.Atoi(port)
					conn, err := proxyDial("tcp", domain, uint16(p))
					if err != nil {
						lastDialErr = err
					}
					return conn, err
				},
			},
		}

		loadFn = func() (io.ReadCloser, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, file, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Add("Accept-Encoding", "gzip")
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil && lastDialErr != nil {
					return nil, fmt.Errorf("proxy dial failed before request cancellation: %v: %w", lastDialErr, ctx.Err())
				}
				return nil, err
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return nil, fmt.Errorf("status code: %d", resp.StatusCode)
			}

			return resp.Body, nil
		}
	}

	// load rule file, retry 10 times
	rc, err := loadFn()
	for i := time.Duration(1); i < 10; i++ {
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("fetch rule file %q canceled after previous error %v: %w", file, err, ctx.Err())
		}

		// wait: 28.5s
		timer := time.NewTimer(i * i * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("fetch rule file %q canceled after previous error %v: %w", file, err, ctx.Err())
		case <-timer.C:
		}
		rc, err = loadFn()
	}
	if err != nil {
		return nil, fmt.Errorf("fetch rule file %q: %w", file, err)
	}

	lines, err := readRuleLines(rc)
	if err != nil {
		return nil, fmt.Errorf("read rule file %q: %w", file, err)
	}
	return lines, nil
}

func readRuleLines(rc io.ReadCloser) ([]string, error) {
	defer rc.Close()

	br := bufio.NewReader(rc)
	reader := io.Reader(br)
	magic, err := br.Peek(2)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, err
	}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	}

	lines := make([]string, 0)
	lineReader := bufio.NewReader(reader)
	for {
		line, err := lineReader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines, nil
		}
	}
}

func closeOnDone(ctx context.Context, wg *sync.WaitGroup, closer io.Closer) {
	defer wg.Done()
	<-ctx.Done()
	_ = closer.Close()
}

func shutdownDNSServerOnDone(ctx context.Context, wg *sync.WaitGroup, server *dns.Server) {
	defer wg.Done()
	<-ctx.Done()
	_ = server.ShutdownContext(ctx)
}

func reportServeError(errCh chan<- error, service string, err error) {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}

	select {
	case errCh <- fmt.Errorf("%s: %w", service, err):
	default:
	}
}

func serveAndReport(errCh chan<- error, service string, serve func() error) {
	reportServeError(errCh, service, serve())
}
