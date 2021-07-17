package main

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"github.com/wweir/sower/pkg/teeconn"
	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
	"github.com/wweir/sower/util"
)

var (
	version, date string

	conf = struct {
		Proxy struct {
			Type     string `default:"sower" usage:"remote proxy protocol, sower/trojan"`
			Addr     string `required:"true" usage:"remote proxy address, eg: proxy.com"`
			Password string `required:"true" usage:"remote proxy password"`
		}

		Socks5Addr  string `default:":1080" usage:"socks5 listen address"`
		FallbackDNS string `default:"223.5.5.5" usage:"fallback dns server"`
		DNSServeIP  string `usage:"dns server ip, eg: 127.0.0.1"`

		Router struct {
			ProxyList  []string
			ProxyRefs  []string
			DirectList []string
			DirectRefs []string
			BlockList  []string
			BlockRefs  []string
		}
	}{}
)

func init() {
	zerolog.ErrorStackMarshaler = func(err error) interface{} {
		return pkgerrors.MarshalStack(err)
	}
	log.Logger = zerolog.New(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.StampMilli,
		FormatCaller: func(i interface{}) string {
			caller := i.(string)
			if idx := strings.Index(caller, "/pkg/mod/"); idx > 0 {
				return caller[idx+9:]
			}
			if idx := strings.LastIndexByte(caller, '/'); idx > 0 {
				return caller[idx+1:]
			}
			return caller
		},
	}).With().Timestamp().Caller().Logger()

	if err := aconfig.LoaderFor(&conf, aconfig.Config{}).Load(); err != nil {
		log.Fatal().Err(err).
			Msg("Load config")
	}

	log.Info().
		Str("version", version).
		Str("date", date).
		Interface("config", conf).
		Msg("Starting")
}

func main() {

	r := router.NewRouter(genProxyDial())

	go func() {
		lnHTTP, err := net.Listen("tcp", net.JoinHostPort(conf.DNSServeIP, "80"))
		if err != nil {
			log.Fatal().Err(err).Msg("listen port")
		}
		go ServeHTTP(lnHTTP, r)

		lnHTTPS, err := net.Listen("tcp", net.JoinHostPort(conf.DNSServeIP, "443"))
		if err != nil {
			log.Fatal().Err(err).Msg("listen port")
		}
		go ServeHTTPS(lnHTTPS, r)

		if err := dns.ListenAndServe(conf.DNSServeIP, "udp", r); err != nil {
			log.Fatal().Err(err).Msg("serve dns")
		}
	}()

	ln, err := net.Listen("tcp", conf.Socks5Addr)
	if err != nil {
		log.Fatal().Err(err).Msg("listen port")
	}
	go ServeSocks5(ln, r)

	select {}
}

func genProxyDial() func(network, host string, port uint16) (net.Conn, error) {
	var (
		proxyAddr = net.JoinHostPort(conf.Proxy.Addr, "443")
		tlsCfg    = &tls.Config{}
		proxy     transport.Transport
	)
	switch conf.Proxy.Type {
	case "sower":
		proxy = sower.New(conf.Proxy.Password)
	case "trojan":
		proxy = trojan.New(conf.Proxy.Password)
	default:
		log.Fatal().
			Str("type", conf.Proxy.Type).
			Msg("unknown proxy type")
	}

	return func(network, host string, port uint16) (net.Conn, error) {
		if host == "" || port == 0 {
			return nil, errors.Errorf("invalid addr(%s:%d)", host, port)
		}

		c, err := tls.Dial("tcp", proxyAddr, tlsCfg)
		if err != nil {
			return nil, err
		}

		if err := proxy.Wrap(c, host, port); err != nil {
			return nil, err
		}

		return c, nil
	}
}

func ServeHTTP(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).
			Msg("serve socks5")
	}

	go ServeHTTP(ln, r)
	start := time.Now()
	teeconn := teeconn.New(conn)
	defer teeconn.Close()

	req, err := http.ReadRequest(bufio.NewReader(teeconn))
	if err != nil {
		log.Error().Err(err).Msg("read http request")
		return
	}

	rc, err := r.ProxyDial("tcp", req.Host, 80)
	if err != nil {
		log.Error().Err(err).
			Str("host", req.Host).
			Interface("req", req.URL).
			Msg("dial proxy")
		return
	}
	defer rc.Close()

	teeconn.Stop().Reread()
	util.Relay(teeconn, rc)
	log.Info().
		Str("host", req.Host).
		Dur("spend", time.Since(start)).
		Msg("serve http")
}

func ServeHTTPS(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).
			Msg("serve socks5")
	}

	go ServeHTTPS(ln, r)
	start := time.Now()
	teeconn := teeconn.New(conn)
	defer teeconn.Close()

	var domain string
	tls.Server(teeconn, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			domain = hello.ServerName
			return nil, nil
		},
	}).Handshake()

	rc, err := r.ProxyDial("tcp", domain, 443)
	if err != nil {
		log.Error().Err(err).
			Str("host", domain).
			Msg("dial proxy")
		return
	}
	defer rc.Close()

	teeconn.Stop().Reread()
	util.Relay(teeconn, rc)
	log.Info().
		Str("host", domain).
		Dur("spend", time.Since(start)).
		Msg("serve http")
}
