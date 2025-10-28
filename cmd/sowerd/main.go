package main

import (
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/lmittmann/tint"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/transport/sower"
	"github.com/sower-proxy/sower/transport/trojan"
	"golang.org/x/crypto/acme/autocert"
)

var (
	version, date string
	conf          config.SowerdConfig
)

func init() {
	fi, _ := os.Stdout.Stat()
	noColor := (fi.Mode() & os.ModeCharDevice) == 0
	deferlog.SetDefault(slog.New(tint.NewHandler(os.Stdout,
		&tint.Options{AddSource: true, NoColor: noColor})))

	err := aconfig.LoaderFor(&conf, aconfig.Config{}).Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	slog.Info("load config", "version", version, "date", date, "config", conf)
}

func main() {
	cacheDir, _ := os.UserCacheDir()
	cacheDir = filepath.Join(cacheDir, "sower")
	if err := os.MkdirAll(cacheDir, 0o600); err != nil {
		slog.Error("create cache dir", "error", err)
		os.Exit(1)
	}

	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Email:  conf.Cert.Email,
		Cache:  autocert.DirCache(cacheDir),
	}

	tlsConf := &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1", "h2"},
	}
	if conf.Cert.Cert != "" || conf.Cert.Key != "" {
		cert, err := tls.LoadX509KeyPair(conf.Cert.Cert, conf.Cert.Key)
		if err != nil {
			slog.Error("load cert", "error", err)
			os.Exit(1)
		}

		tlsConf.GetCertificate = nil
		tlsConf.Certificates = []tls.Certificate{cert}
	}

	// Redirect 80 to 443, and serve fake site if it's a directory.
	addr := net.JoinHostPort(conf.ServeIP, "80")
	var dirServer http.Handler
	if si, err := os.Stat(conf.FakeSite); err == nil && si.IsDir() {
		slog.Info("serve fake site on http", "dir", conf.FakeSite)
		dirServer = http.FileServer(http.Dir(conf.FakeSite))
		conf.FakeSite = "127.0.0.1:80"
	}
	go http.ListenAndServe(addr, certManager.HTTPHandler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if dirServer != nil && strings.HasPrefix(r.RemoteAddr, "127.0.0.1:") {
				dirServer.ServeHTTP(w, r)
			} else {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
			}
		})))

	ln, err := tls.Listen("tcp", net.JoinHostPort(conf.ServeIP, "443"), tlsConf)
	if err != nil {
		slog.Error("listen https", "error", err)
		os.Exit(1)
	}

	go serve443(ln, conf.FakeSite, sower.New(conf.Password), trojan.New(conf.Password))
	select {}
}

func serve443(ln net.Listener, fakeSite string, sower *sower.Sower, trojan *trojan.Trojan) {
	conn, err := ln.Accept()
	if err != nil {
		slog.Error("serve 443 port", "error", err)
		os.Exit(1)
	}
	go serve443(ln, fakeSite, sower, trojan)
	reread := reread.New(conn)
	defer reread.Close()

	var addr net.Addr
	var dur time.Duration
	defer func() {
		deferlog.DebugWarn(err, "relay conn", "took", dur, "addr", addr)
	}()

	// 1. detect if it's a sower underlaying connection
	reread.Reread()
	if addr, err = sower.Unwrap(reread); err == nil {
		reread.Stop()

		dur, err = relay.RelayTo(reread, addr.String())
		return
	}

	// 2. detect if it's a trojan underlaying connection
	reread.Reread()
	if addr, err = trojan.Unwrap(reread); err == nil {
		reread.Stop()

		dur, err = relay.RelayTo(reread, addr.String())
		return
	}

	// 3. fallback to fake site
	reread.Stop().Reread()
	dur, err = relay.RelayTo(reread, fakeSite)
}
