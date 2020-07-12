package conf

import (
	"bufio"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	toml "github.com/pelletier/go-toml"
	"github.com/wweir/sower/util"
	"github.com/wweir/util-go/log"
	"golang.org/x/xerrors"
)

type Client struct {
	Address     string            `toml:"address"`
	DNSUpstream string            `toml:"dns_upstream"`
	Socks5Proxy string            `toml:"socks5"`
	HTTPProxy   string            `toml:"http_proxy"`
	PortForward map[string]string `toml:"port_forward"`

	Router struct {
		DetectLevel int      `toml:"detect_level"`
		ProxyList   []string `toml:"proxy_list"`
		ProxyRefs   []string `toml:"proxy_refs"`
		DirectList  []string `toml:"direct_list"`
		DirectRefs  []string `toml:"direct_refs"`
		BlockList   []string `toml:"block_list"`
		BlockRefs   []string `toml:"block_refs"`
	} `toml:"router"`
}
type Server struct {
	Upstream  string `toml:"upstream"`
	CertFile  string `toml:"cert_file"`
	KeyFile   string `toml:"key_file"`
	CertEmail string `toml:"cert_email"`
}

var (
	version, date string

	execFile, _ = os.Executable()
	execDir, _  = filepath.Abs(filepath.Dir(execFile))
	// Conf full config, include common and server / client
	conf = struct {
		file     string
		Password string `toml:"password"`
		Client   Client `toml:"client"`
		Server   Server `toml:"server"`
	}{}
)

func Init() (*Client, *Server, string) {
	beforeInitFlag()
	defer afterInitFlag()
	flag.StringVar(&conf.Password, "password", "", "password")
	flag.StringVar(&conf.Server.Upstream, "s", "", "upstream http service, eg: 127.0.0.1:8080")
	flag.StringVar(&conf.Server.CertFile, "s_cert", "", "tls cert file, gen cert from letsencrypt if empty")
	flag.StringVar(&conf.Server.KeyFile, "s_key", "", "tls key file, gen cert from letsencrypt if empty")
	flag.StringVar(&conf.Client.Address, "c", "", "remote server domain, eg: aa.bb.cc, socks5h://127.0.0.1:1080")
	flag.StringVar(&conf.Client.HTTPProxy, "http_proxy", ":8080", "http proxy, empty to disable")

	if !flag.Parsed() {
		flag.Parse()
	}

	defer log.Infow("starting", "version", version, "date", date, "config", &conf)
	if conf.file == "" {
		return &conf.Client, &conf.Server, conf.Password
	}

	for i := range loadConfigFns {
		if err := loadConfigFns[i].fn(); err != nil {
			log.Fatalw("load config", "config", conf.file, "step", loadConfigFns[i].step, "err", err)
		}
	}
	return &conf.Client, &conf.Server, conf.Password
}

// refreshFns will be executed while init and write new config
var loadConfigFns = []struct {
	step string
	fn   func() error
}{{"parse file", func() error {
	f, err := os.OpenFile(conf.file, os.O_RDONLY, 0644)
	if err != nil {
		return xerrors.New(err.Error())
	}
	defer f.Close()

	return toml.NewDecoder(f).Decode(&conf)
}}, {"load referenced rule", func() error {
	for _, addr := range conf.Client.Router.BlockRefs {
		lines, err := getRemoteRuleLines(addr)
		if err != nil {
			return err
		}
		conf.Client.Router.BlockList = append(conf.Client.Router.BlockList, lines...)
	}
	for _, addr := range conf.Client.Router.ProxyRefs {
		lines, err := getRemoteRuleLines(addr)
		if err != nil {
			return err
		}
		conf.Client.Router.ProxyList = append(conf.Client.Router.ProxyList, lines...)
	}
	for _, addr := range conf.Client.Router.DirectRefs {
		lines, err := getRemoteRuleLines(addr)
		if err != nil {
			return err
		}
		conf.Client.Router.DirectList = append(conf.Client.Router.DirectList, lines...)
	}

	return nil
}}}

func getRemoteRuleLines(addr string) ([]string, error) {
	resp, err := http.Get(addr)
	if err != nil {
		return nil, xerrors.New(err.Error())
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	lines := []string{}
	for {
		line, _, err := br.ReadLine()
		if err == io.EOF {
			return lines, nil
		} else if err != nil {
			return nil, xerrors.New(err.Error())
		}

		lines = append(lines, "**."+strings.TrimSpace(string(line)))
	}
}

// flushCh to avoid parallel persist
var flushCh = make(chan struct{})
var flushOnce = sync.Once{}

// PersistRule persist rule into config file
func PersistRule(domain string) {
	flushOnce.Do(func() {
		go flushConfDaemon()
	})

	log.Infow("persist direct rule into config", "domain", domain)
	conf.Client.Router.DirectList = append(conf.Client.Router.DirectList, domain)
	select {
	case flushCh <- struct{}{}:
	default:
	}
}
func flushConfDaemon() {
	for range flushCh {
		// safe write file
		if conf.file != "" {
			f, err := os.OpenFile(conf.file+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}

			conf.Client.Router.DirectList =
				util.NewReverseSecSlice(conf.Client.Router.DirectList).Sort().Uniq()

			if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(&conf); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				f.Close()
				continue
			}
			f.Close()

			if stat, err := os.Stat(conf.file); err != nil {
				log.Warnw("get file stat", "file", conf.file, "err", err)
			} else {
				// There is no common way to transfer ownership for a file
				// cross-platform. Drop the ownership support but file mod.
				if err = os.Chmod(conf.file+"~", stat.Mode()); err != nil {
					log.Warnw("set file mod", "file", conf.file+"~", "err", err)
				}
			}

			if err = os.Rename(conf.file+"~", conf.file); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}
		}
	}
}
