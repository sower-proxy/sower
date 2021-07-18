package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigdotenv"
	"github.com/cristalhq/aconfig/aconfighcl"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/router"
)

var (
	version, date string

	conf = struct {
		Remote struct {
			Type     string `default:"sower" usage:"remote proxy protocol, sower/trojan"`
			Addr     string `required:"true" usage:"remote proxy address, eg: proxy.com"`
			Password string `required:"true" usage:"remote proxy password"`
		}

		DNS struct {
			Enable   bool   `default:"true" usage:"enable DNS proxy"`
			Serve    string `usage:"dns server ip, default all, eg: 127.0.0.1"`
			Fallback string `default:"223.5.5.5" usage:"fallback dns server"`
		}
		Socks5 struct {
			Enable bool   `default:"true" usage:"enable sock5 proxy"`
			Addr   string `default:":1080" usage:"socks5 listen address"`
		} `flag:"socks5"`

		Router struct {
			Block struct {
				File  string   `usage:"block list file, parsed as '**.line_text'"`
				Rules []string `usage:"block list rules"`
			}
			Direct struct {
				File  string   `usage:"direct list file, parsed as '**.line_text'"`
				Rules []string `usage:"direct list rules"`
			}
			Proxy struct {
				File  string   `usage:"proxy list file, parsed as '**.line_text'"`
				Rules []string `usage:"proxy list rules"`
			}

			Country struct {
				MMDB  string   `usage:"mmdb file"`
				File  string   `usage:"CIDR block list file"`
				Rules []string `usage:"CIDR list rules"`
			}
		}
	}{}
)

func init() {
	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		FileFlag: "conf",
		Files: []string{".env",
			"config.yml", "config.yaml", "config.json", "config.toml", "config.hcl"},
		FileDecoders: map[string]aconfig.FileDecoder{
			".env":  aconfigdotenv.New(),
			".yml":  aconfigyaml.New(),
			".yaml": aconfigyaml.New(),
			".toml": aconfigtoml.New(),
			".hcl":  aconfighcl.New(),
			".tf":   aconfighcl.New(),
		},
	}).Load(); err != nil {
		log.Fatal().Err(err).
			Interface("conf", conf).
			Msg("Load config")
	}

	log.Info().
		Str("version", version).
		Str("date", date).
		Interface("config", conf).
		Msg("Starting")
}

func main() {
	proxtDial := GenProxyDial(conf.Remote.Type, conf.Remote.Addr, conf.Remote.Password)
	r := router.NewRouter(conf.DNS.Fallback, conf.Router.Country.MMDB, proxtDial,
		append(conf.Router.Block.Rules, parseRuleLines(proxtDial, conf.Router.Block.File, "**.")...),
		append(conf.Router.Direct.Rules, parseRuleLines(proxtDial, conf.Router.Direct.File, "**.")...),
		append(conf.Router.Proxy.Rules, parseRuleLines(proxtDial, conf.Router.Proxy.File, "**.")...),
		append(conf.Router.Country.Rules, parseRuleLines(proxtDial, conf.Router.Country.File, "")...),
	)

	go func() {
		if !conf.DNS.Enable {
			log.Info().Msg("DNS proxy disabled")
			return
		}

		lnHTTP, err := net.Listen("tcp", net.JoinHostPort(conf.DNS.Serve, "80"))
		if err != nil {
			log.Fatal().Err(err).Msg("listen port")
		}
		go ServeHTTP(lnHTTP, r)

		lnHTTPS, err := net.Listen("tcp", net.JoinHostPort(conf.DNS.Serve, "443"))
		if err != nil {
			log.Fatal().Err(err).Msg("listen port")
		}
		go ServeHTTPS(lnHTTPS, r)

		log.Info().Msg("DNS proxy started")
		if err := dns.ListenAndServe(conf.DNS.Serve, "udp", r); err != nil {
			log.Fatal().Err(err).Msg("serve dns")
		}
	}()

	go func() {
		if !conf.Socks5.Enable {
			log.Info().Msg("SOCKS5 proxy disabled")
			return
		}

		ln, err := net.Listen("tcp", conf.Socks5.Addr)
		if err != nil {
			log.Fatal().Err(err).Msg("listen port")
		}
		log.Info().Msgf("SOCKS5 proxy listening on %s", conf.Socks5.Addr)
		ServeSocks5(ln, r)
	}()

	select {}
}

func parseRuleLines(proxyDial router.ProxyDialFn, file, linePrefix string) []string {
	var r io.Reader
	if _, err := url.Parse(file); err == nil {
		client := &http.Client{
			Transport: &http.Transport{
				Dial: func(network, addr string) (net.Conn, error) {
					domain, port, _ := net.SplitHostPort(addr)
					p, _ := strconv.Atoi(port)
					return proxyDial("tcp", domain, uint16(p))
				},
			},
		}

		resp, err := client.Get(file)
		if err != nil {
			log.Error().Err(err).
				Str("file", file).
				Msg("proxy read response")
			return nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Error().
				Int("status", resp.StatusCode).
				Str("file", file).
				Msg("proxy response status")
			return nil
		}

		r = resp.Body

	} else {
		f, err := os.Open(file)
		if err != nil {
			log.Error().Err(err).
				Str("file", file).
				Msg("open file")
			return nil
		}
		defer f.Close()

		r = f
	}

	var lines []string
	br := bufio.NewReader(r)
	for {
		line, _, err := br.ReadLine()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Error().Err(err).
				Str("file", file).
				Msg("read line")
			return nil
		}

		if strings.TrimSpace(string(line)) == "" {
			continue
		}

		// use line content as suffix
		lines = append(lines, linePrefix+string(line))
	}

	return lines
}
