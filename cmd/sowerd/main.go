package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/deferlog"
	"github.com/sower-proxy/deferlog/log"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
	"golang.org/x/crypto/acme/autocert"
)

var (
	version, date string

	conf = struct {
		ServeIP  string `usage:"listen to port 80 443 of this IP, eg: 0.0.0.0"`
		Password string `required:"true"`
		FakeSite string `default:"127.0.0.1:8080" usage:"fake site address or directoy"`

		Cert struct {
			Email string
			Cert  string
			Key   string
		}
	}{}
)

func init() {
	err := aconfig.LoaderFor(&conf, aconfig.Config{}).Load()
	log.InfoFatal(err).
		Str("version", version).
		Str("date", date).
		Interface("config", conf).
		Msg("Load config")
}

func main() {
	cacheDir, _ := os.UserCacheDir()
	cacheDir = filepath.Join(cacheDir, "sower")
	if err := os.MkdirAll(cacheDir, 0o600); err != nil {
		log.Fatal().Err(err).
			Str("dir", cacheDir).
			Msg("make cache dir")
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
			log.Fatal().Err(err).Msg("load certificate")
		}

		tlsConf.GetCertificate = nil
		tlsConf.Certificates = []tls.Certificate{cert}
	}

	// Redirect 80 to 443
	go func() {
		err := http.ListenAndServe(net.JoinHostPort(conf.ServeIP, "80"),
			certManager.HTTPHandler(http.HandlerFunc(redirectToHTTPS)))
		log.Fatal().Err(err).
			Str("IP", conf.ServeIP).
			Msg("Listen HTTP service")
	}()

	ln, err := tls.Listen("tcp", net.JoinHostPort(conf.ServeIP, "443"), tlsConf)
	log.InfoFatal(err).
		Str("IP", conf.ServeIP).
		Msg("Start listen HTTPS service")

	if si, err := os.Stat(conf.FakeSite); err == nil && si.IsDir() {
		http.NewFileTransport(http.Dir(conf.FakeSite))
	} else if _, _, err := net.SplitHostPort(conf.FakeSite); err == nil {
		go serve443(ln, conf.FakeSite, sower.New(conf.Password), trojan.New(conf.Password))
	}

	select {}
}

func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	r.URL.Scheme = "https"
	if host, _, err := net.SplitHostPort(r.Host); err != nil {
		r.URL.Host = r.Host
	} else {
		r.URL.Host = host
	}

	http.Redirect(w, r, r.URL.String(), 301)
}

func serve443(ln net.Listener, fakeSite string, sower *sower.Sower, trojan *trojan.Trojan) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).Msg("serve 443 port")
	}
	go serve443(ln, fakeSite, sower, trojan)
	reread := reread.New(conn)
	defer reread.Close()

	var addr net.Addr
	var dur time.Duration
	defer func() {
		deferlog.DebugWarn(err).
			Dur("spend", dur).
			Msgf("relay conn to %s", addr)
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
