package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/lmittmann/tint"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/install"
	transportSower "github.com/sower-proxy/sower/transport/sower"
	"golang.org/x/crypto/acme/autocert"
)

const (
	httpShutdownTimeout           = 5 * time.Second
	reverseProxyReadHeaderTimeout = 10 * time.Second
	reverseProxyIdleTimeout       = 30 * time.Second
	upstreamDialTimeout           = 10 * time.Second
	upstreamResponseTimeout       = 30 * time.Second
	systemCacheDir                = "/var/cache/sower"
)

var (
	version, date string
)

func init() {
	setLogger(slog.LevelInfo)
}

func main() {
	if hasInstallFlag(os.Args[1:]) {
		if err := install.InstallService(stdinConfirm); err != nil {
			slog.Error("install service", "error", err)
			os.Exit(1)
		}
		return
	}

	conf, err := loadConfig()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	setLogger(conf.LogLevel)
	slog.Info("load config",
		"version", version,
		"date", date,
		"config", sanitizeConfig(conf))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, conf); err != nil {
		slog.Error("sowerd exited", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config.SowerdConfig, error) {
	var conf config.SowerdConfig
	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		AllowUnknownFlags:  false,
		AllowUnknownFields: false,
		FileFlag:           "c",
		Files: []string{
			"sowerd.toml",
			"config/sowerd.toml",
			"/etc/sower/sowerd.toml",
		},
		FileDecoders: map[string]aconfig.FileDecoder{
			".toml": aconfigtoml.New(),
		},
	}).Load(); err != nil {
		return config.SowerdConfig{}, err
	}
	if err := conf.Validate(); err != nil {
		return config.SowerdConfig{}, err
	}
	return conf, nil
}

func run(ctx context.Context, conf config.SowerdConfig) error {
	cacheDir, err := cacheDir()
	if err != nil {
		return err
	}

	certManager, tlsConf, err := buildTLSConfig(cacheDir, conf)
	if err != nil {
		return err
	}

	fakeSite, dirServer, err := prepareFakeSite(conf.FakeSite, conf.ServeIP)
	if err != nil {
		return err
	}

	siteRouter := newSiteRouter(conf.SiteRoutes)

	httpAddr := net.JoinHostPort(conf.ServeIP, "80")
	httpServer := &http.Server{
		Addr:    httpAddr,
		Handler: certManager.HTTPHandler(fakeSiteHandler(dirServer)),
	}

	httpErrCh := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrCh <- fmt.Errorf("listen http %s: %w", httpAddr, err)
		}
		close(httpErrCh)
	}()

	httpsAddr := net.JoinHostPort(conf.ServeIP, "443")
	ln, err := tls.Listen("tcp", httpsAddr, tlsConf)
	if err != nil {
		_ = shutdownHTTP(context.Background(), httpServer)
		return fmt.Errorf("listen https %s: %w", httpsAddr, err)
	}
	defer ln.Close()

	protocolHandlers := []proxyProtocolHandler{
		newSowerProtocolHandler(transportSower.New(conf.Password)),
	}

	httpsErrCh := make(chan error, 1)
	go func() {
		httpsErrCh <- serve443(ctx, ln, fakeSite, siteRouter, protocolHandlers)
		close(httpsErrCh)
	}()

	select {
	case err := <-httpErrCh:
		if err != nil {
			_ = ln.Close()
			return err
		}
	case err := <-httpsErrCh:
		if err != nil {
			_ = shutdownHTTP(context.Background(), httpServer)
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()

	_ = ln.Close()
	if err := shutdownHTTP(shutdownCtx, httpServer); err != nil {
		return err
	}
	return nil
}

func cacheDir() (string, error) {
	dir, fallbackErr := resolveCacheDir(os.UserCacheDir, systemCacheDir)
	if fallbackErr != nil {
		slog.Warn("user cache dir unavailable, fallback to system cache dir",
			"error", fallbackErr,
			"dir", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return dir, nil
}

func resolveCacheDir(userCacheDir func() (string, error), fallbackDir string) (string, error) {
	base, err := userCacheDir()
	if err != nil {
		return fallbackDir, err
	}
	return filepath.Join(base, "sower"), nil
}

func buildTLSConfig(cacheDir string, cfg config.SowerdConfig) (*autocert.Manager, *tls.Config, error) {
	certManager := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Email:  cfg.Cert.Email,
		Cache:  autocert.DirCache(cacheDir),
		// Only issue for names the operator owns: every site-routed domain
		// plus the explicit cert.domains whitelist. A nil HostPolicy lets
		// anyone trigger issuance for arbitrary SNI names, exhausting the
		// account's order/rate limits and polluting the cache.
		HostPolicy: autocert.HostWhitelist(certIssueDomains(cfg)...),
	}

	tlsConf := &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
	}

	if cfg.Cert.Cert == "" {
		return certManager, tlsConf, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.Cert.Cert, cfg.Cert.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("load cert pair: %w", err)
	}

	tlsConf.GetCertificate = nil
	tlsConf.Certificates = []tls.Certificate{cert}
	return certManager, tlsConf, nil
}

// certIssueDomains collects the deduplicated domain whitelist for autocert
// issuance: every site_routes domain plus cert.domains.
func certIssueDomains(cfg config.SowerdConfig) []string {
	seen := make(map[string]struct{}, len(cfg.Cert.Domains))
	domains := make([]string, 0, len(cfg.Cert.Domains))
	add := func(d string) {
		d = strings.ToLower(d)
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		domains = append(domains, d)
	}
	for _, d := range cfg.Cert.Domains {
		add(d)
	}
	for _, route := range cfg.SiteRoutes {
		for _, d := range route.Domains {
			add(d)
		}
	}
	return domains
}

func prepareFakeSite(fakeSite string, serveIP string) (string, http.Handler, error) {
	si, err := os.Stat(fakeSite)
	if err == nil && si.IsDir() {
		slog.Info("serve fake site on http", "dir", fakeSite)
		// The directory fake site is served by the loopback HTTP server.
		// Prefer a concrete serve_ip as the relay target (its :80 listener
		// exists); otherwise fall back to the loopback address.
		target := "127.0.0.1:80"
		if ip := net.ParseIP(serveIP); ip != nil && !ip.IsUnspecified() {
			target = net.JoinHostPort(serveIP, "80")
		}
		return target, http.FileServer(http.Dir(fakeSite)), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("stat fake site: %w", err)
	}
	return fakeSite, nil, nil
}

func fakeSiteHandler(dirServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dirServer != nil && isLocalRemoteAddr(r.RemoteAddr) {
			dirServer.ServeHTTP(w, r)
			return
		}

		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func serve443(ctx context.Context, ln net.Listener, fakeSite string, router siteRouter, handlers []proxyProtocolHandler) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				slog.Warn("temporary accept error", "error", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept tls connection: %w", err)
		}

		go handleConn(conn, fakeSite, router, handlers)
	}
}

func handleConn(conn net.Conn, fakeSite string, router siteRouter, handlers []proxyProtocolHandler) {
	rereadConn := reread.New(conn)
	var hijacked atomic.Bool
	defer func() {
		if !hijacked.Load() {
			rereadConn.Close()
		}
	}()

	// Complete the TLS handshake explicitly before probing: on first
	// contact the handshake can take tens of seconds while GetCertificate
	// drives ACME issuance, and the short probe deadline must not race it
	// (the first connection to a new domain would otherwise always fail).
	if tlsConn, ok := conn.(*tls.Conn); ok {
		handshakeCtx, cancel := context.WithTimeout(context.Background(), tlsHandshakeTimeout)
		if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
			cancel()
			return
		}
		cancel()
	}

	_ = rereadConn.SetReadDeadline(time.Now().Add(protocolProbeTimeout))

	var (
		addr net.Addr
		dur  time.Duration
		err  error
	)
	defer func() {
		deferlog.DebugWarn(err, "relay conn", "took", dur, "addr", addr)
	}()

	rereadConn.Reread()
	probeBuf, err := readProtocolProbe(rereadConn)
	if err != nil {
		return
	}

	for _, handler := range handlers {
		switch handler.Probe(probeBuf) {
		case probeNoMatch, probeNeedMore:
			continue
		case probeMatch:
			rereadConn.Reread()
			// Bound the header read with its own (longer than the probe)
			// deadline: the header follows the TLS handshake immediately,
			// but a slow client needs more than the 1s probe window, and an
			// attacker that sends only the 0x80 probe byte must not hold
			// the connection (and its goroutine) forever.
			_ = rereadConn.SetReadDeadline(time.Now().Add(protocolHeaderTimeout))
			if addr, err = handler.Unwrap(rereadConn); err == nil {
				_ = rereadConn.SetReadDeadline(time.Time{})
				rereadConn.Stop()
				dur, err = relay.RelayTo(rereadConn, addr.String())
				return
			}

			slog.Debug("protocol auth or decode failed, fallback", "protocol", handler.Name(), "error", err)
			_ = rereadConn.SetReadDeadline(time.Time{})
			rereadConn.Stop().Reread()
			dur, err = fallbackConn(rereadConn, conn, fakeSite, router, &hijacked)
			return
		}
	}

	rereadConn.Stop().Reread()
	_ = rereadConn.SetReadDeadline(time.Time{})
	dur, err = fallbackConn(rereadConn, conn, fakeSite, router, &hijacked)
}

func fallbackConn(conn net.Conn, tlsConn net.Conn, fakeSite string, router siteRouter, hijacked *atomic.Bool) (time.Duration, error) {
	start := time.Now()
	if upstream := router.lookup(sniFromConn(tlsConn)); upstream != nil {
		return time.Since(start), reverseProxyConn(conn, upstream, hijacked)
	}
	return relay.RelayTo(conn, fakeSite)
}

func sanitizeConfig(cfg config.SowerdConfig) map[string]any {
	return map[string]any{
		"log_level":   cfg.LogLevel.String(),
		"serve_ip":    cfg.ServeIP,
		"fake_site":   cfg.FakeSite,
		"site_routes": len(cfg.SiteRoutes),
		"cert": map[string]any{
			"email":       cfg.Cert.Email,
			"cert_config": cfg.Cert.Cert != "",
			"key_config":  cfg.Cert.Key != "",
		},
	}
}

func hasInstallFlag(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-i", "--install":
			return true
		}
	}
	return false
}

func stdinConfirm(label string) bool {
	fmt.Printf("%s [y/N]: ", label)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// isLocalRemoteAddr reports whether the connection originated on this host:
// loopback or an address bound to a local interface. Directory fake sites
// relay fallback traffic from this process; the relay's source address
// follows the target (loopback for the default 0.0.0.0 serve_ip, the
// serve_ip itself for a concrete bind), so a loopback-only check would
// wrongly redirect concrete serve_ip deployments.
func isLocalRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func setLogger(level slog.Level) {
	fi, err := os.Stdout.Stat()
	noColor := err != nil || (fi.Mode()&os.ModeCharDevice) == 0
	deferlog.SetDefault(slog.New(tint.NewHandler(os.Stdout, &tint.Options{
		AddSource: true,
		NoColor:   noColor,
		Level:     level,
	})))
}

func shutdownHTTP(ctx context.Context, server *http.Server) error {
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}

// siteRouter maps TLS SNI to an upstream URL for fallback traffic.
type siteRouter struct {
	routes map[string]*url.URL
}

func newSiteRouter(routes []config.SiteRoute) siteRouter {
	m := make(map[string]*url.URL)
	for _, r := range routes {
		u, _ := url.Parse(r.Upstream) // validated in config.Validate
		for _, d := range r.Domains {
			m[strings.ToLower(d)] = u
		}
	}
	return siteRouter{routes: m}
}

// lookup returns the upstream URL for the given SNI, or nil if no route matches.
func (r siteRouter) lookup(sni string) *url.URL {
	return r.routes[strings.ToLower(sni)]
}

// sniFromConn extracts the TLS SNI from a *tls.Conn.
func sniFromConn(conn net.Conn) string {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return ""
	}
	return tlsConn.ConnectionState().ServerName
}

// reverseProxyConn serves the decrypted HTTP connection through a reverse proxy
// to the given upstream URL.
func reverseProxyConn(conn net.Conn, upstream *url.URL, hijacked *atomic.Bool) error {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Host = upstream.Host
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   upstreamDialTimeout,
			KeepAlive: upstreamResponseTimeout,
		}).DialContext,
		TLSHandshakeTimeout:   upstreamDialTimeout,
		ResponseHeaderTimeout: upstreamResponseTimeout,
		IdleConnTimeout:       reverseProxyIdleTimeout,
		ExpectContinueTimeout: time.Second,
	}
	defer transport.CloseIdleConnections()
	proxy.Transport = transport
	proxy.ErrorLog = slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)

	ln := newSingleConnListener(conn)
	srv := &http.Server{
		Handler:           proxy,
		ReadHeaderTimeout: reverseProxyReadHeaderTimeout,
		IdleTimeout:       reverseProxyIdleTimeout,
		ConnState: func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateHijacked:
				hijacked.Store(true)
				_ = ln.Close()
			case http.StateClosed:
				_ = ln.Close()
			}
		},
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve reverse proxy connection: %w", err)
	}
	return nil
}

// singleConnListener wraps a single net.Conn as a net.Listener.
// Accept returns the conn once, then blocks until Close is called.
type singleConnListener struct {
	conn      net.Conn
	once      bool
	closed    chan struct{}
	closeOnce sync.Once
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if !l.once {
		l.once = true
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
