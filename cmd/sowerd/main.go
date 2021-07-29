package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cristalhq/aconfig"
	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/pkg/teeconn"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
	"github.com/wweir/sower/util"
	"golang.org/x/crypto/acme/autocert"
)

var (
	version, date string

	conf = struct {
		ServeIP  string `usage:"listen to port 80 443 of this IP, eg: 0.0.0.0"`
		Password string `required:"true"`
		FakeSite string `required:"true" default:"127.0.0.1:8080" usage:"fake site address"`

		Cert struct {
			Email string
			Cert  string
			Key   string
		}
	}{}
)

func init() {
	if err := aconfig.LoaderFor(&conf, aconfig.Config{}).Load(); err != nil {
		log.Fatal().Err(err).Msg("Load config")
	}

	log.Info().
		Str("version", version).
		Str("date", date).
		Interface("config", conf).
		Msg("Starting")
}

func main() {
	cacheDir, _ := os.UserCacheDir()
	cacheDir = filepath.Join(cacheDir, "sower")
	if err := os.MkdirAll(cacheDir, 0600); err != nil {
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
	go http.ListenAndServe(net.JoinHostPort(conf.ServeIP, "80"),
		certManager.HTTPHandler(http.HandlerFunc(redirectToHTTPS)))

	ln, err := tls.Listen("tcp", net.JoinHostPort(conf.ServeIP, "443"), tlsConf)
	if err != nil {
		log.Fatal().Err(err).Msg("listen tcp 443")
	}

	go serve443(ln, conf.FakeSite, sower.New(conf.Password), trojan.New(conf.Password))
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
	teeconn := teeconn.New(conn)
	defer teeconn.Close()

	teeconn.Reread()
	if addr, err := sower.Unwrap(teeconn); err == nil {
		teeconn.Stop()

		dur, err := util.RelayTo(teeconn, addr.String())
		log.Err(err).
			Dur("spend", dur).
			Str("target", addr.String()).
			Msg("relay sower conn")
		return
	}

	teeconn.Reread()
	if addr, err := trojan.Unwrap(teeconn); err == nil {
		teeconn.Stop()

		dur, err := util.RelayTo(teeconn, addr.String())
		log.Err(err).
			Dur("spend", dur).
			Str("target", addr.String()).
			Msg("relay trojan conn")
		return
	}

	teeconn.Stop().Reread()
	dur, err := util.RelayTo(teeconn, fakeSite)
	log.Err(err).
		Dur("spend", dur).
		Str("target", fakeSite).
		Msg("relay fake site")
}
