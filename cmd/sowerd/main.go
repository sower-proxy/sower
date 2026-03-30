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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/lmittmann/tint"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/install"
	transportSower "github.com/sower-proxy/sower/transport/sower"
	"github.com/sower-proxy/sower/transport/trojan"
	"golang.org/x/crypto/acme/autocert"
)

const (
	httpShutdownTimeout = 5 * time.Second
	probeTimeout        = 10 * time.Second
	systemCacheDir      = "/var/cache/sower"
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
			"sowerd.yaml",
			"sowerd.yml",
			"config/sowerd.toml",
			"config/sowerd.yaml",
			"config/sowerd.yml",
			"/etc/sower/sowerd.toml",
			"/etc/sower/sowerd.yaml",
			"/etc/sower/sowerd.yml",
		},
		FileDecoders: map[string]aconfig.FileDecoder{
			".yml":  aconfigyaml.New(),
			".yaml": aconfigyaml.New(),
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

	fakeSite, dirServer, err := prepareFakeSite(conf.FakeSite)
	if err != nil {
		return err
	}

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

	httpsErrCh := make(chan error, 1)
	go func() {
		httpsErrCh <- serve443(ctx, ln, fakeSite, transportSower.New(conf.Password), trojan.New(conf.Password))
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
	}

	tlsConf := &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1", "h2"},
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

func prepareFakeSite(fakeSite string) (string, http.Handler, error) {
	si, err := os.Stat(fakeSite)
	if err == nil && si.IsDir() {
		slog.Info("serve fake site on http", "dir", fakeSite)
		return "127.0.0.1:80", http.FileServer(http.Dir(fakeSite)), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("stat fake site: %w", err)
	}
	return fakeSite, nil, nil
}

func fakeSiteHandler(dirServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dirServer != nil && isLoopbackRemoteAddr(r.RemoteAddr) {
			dirServer.ServeHTTP(w, r)
			return
		}

		target := "https://" + r.Host + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func serve443(ctx context.Context, ln net.Listener, fakeSite string, sower *transportSower.Sower, trojan *trojan.Trojan) error {
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

		go handleConn(conn, fakeSite, sower, trojan)
	}
}

func handleConn(conn net.Conn, fakeSite string, sower *transportSower.Sower, trojan *trojan.Trojan) {
	rereadConn := reread.New(conn)
	defer rereadConn.Close()

	_ = rereadConn.SetReadDeadline(time.Now().Add(probeTimeout))

	var (
		addr net.Addr
		dur  time.Duration
		err  error
	)
	defer func() {
		deferlog.DebugWarn(err, "relay conn", "took", dur, "addr", addr)
	}()

	rereadConn.Reread()
	if addr, err = sower.Unwrap(rereadConn); err == nil {
		rereadConn.Stop()
		_ = rereadConn.SetReadDeadline(time.Time{})
		dur, err = relay.RelayTo(rereadConn, addr.String())
		return
	}

	rereadConn.Reread()
	if addr, err = trojan.Unwrap(rereadConn); err == nil {
		rereadConn.Stop()
		_ = rereadConn.SetReadDeadline(time.Time{})
		dur, err = relay.RelayTo(rereadConn, addr.String())
		return
	}

	rereadConn.Stop().Reread()
	_ = rereadConn.SetReadDeadline(time.Time{})
	dur, err = relay.RelayTo(rereadConn, fakeSite)
}

func sanitizeConfig(cfg config.SowerdConfig) map[string]any {
	return map[string]any{
		"log_level": cfg.LogLevel.String(),
		"serve_ip":  cfg.ServeIP,
		"fake_site": cfg.FakeSite,
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

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
