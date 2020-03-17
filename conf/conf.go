package conf

import (
	"flag"
	"os"

	toml "github.com/pelletier/go-toml"
	"github.com/wweir/utils/log"
)

type client struct {
	Address     string            `toml:"address"`
	HTTPProxy   string            `toml:"http_proxy"`
	DNSServeIP  string            `toml:"dns_serve_ip"`
	DNSUpstream string            `toml:"dns_upstream"`
	PortForward map[string]string `toml:"port_forward"`

	Router struct {
		DetectLevel   int    `toml:"detect_level"`
		DetectTimeout string `toml:"detect_timeout"`

		ProxyList   []string `toml:"proxy_list"`
		DirectList  []string `toml:"direct_list"`
		DynamicList []string `toml:"dynamic_list"`
	} `toml:"router"`
}
type server struct {
	Upstream  string `toml:"upstream"`
	CertFile  string `toml:"cert_file"`
	KeyFile   string `toml:"key_file"`
	CertEmail string `toml:"cert_email"`
}

var (
	version, date string

	Server = server{}
	Client = client{}
	Conf   = struct {
		file     string
		Password string  `toml:"password"`
		Server   *server `toml:"server"`
		Client   *client `toml:"client"`
	}{"", "", &Server, &Client}
	flushCh       = make(chan struct{})
	installCmd    string
	uninstallFlag bool
)

func init() {
	flag.StringVar(&Conf.Password, "password", "", "password")
	flag.StringVar(&Server.Upstream, "s", "", "upstream http service, eg: 127.0.0.1:8080")
	flag.StringVar(&Server.CertFile, "s_cert", "", "tls cert file, gen cert from letsencrypt if empty")
	flag.StringVar(&Server.KeyFile, "s_key", "", "tls key file, gen cert from letsencrypt if empty")
	flag.StringVar(&Client.Address, "c", "", "remote server domain, eg: aa.bb.cc, socks5h://127.0.0.1:1080")
	flag.StringVar(&Client.HTTPProxy, "http_proxy", ":8080", "http proxy, empty to disable")
	flag.IntVar(&Client.Router.DetectLevel, "level", 2, "dynamic rule detect level: 0~4")
	flag.StringVar(&Client.Router.DetectTimeout, "timeout", "300ms", "dynamic rule detect timeout")
	flag.BoolVar(&uninstallFlag, "uninstall", false, "uninstall service")
	Init() // execute platform init logic

	if !flag.Parsed() {
		flag.Parse()
	}
	if uninstallFlag {
		uninstall()
		os.Exit(0)
	}
	if installCmd != "" {
		install()
		os.Exit(0)
	}

	defer log.Infow("starting", "config", &Conf)
	if Conf.file == "" {
		return
	}

	for i := range loadConfigFns {
		if err := loadConfigFns[i].fn(); err != nil {
			log.Fatalw("load config", "config", Conf.file, "step", loadConfigFns[i].step, "err", err)
		}
	}

	go flushConfDaemon()
}

// refreshFns will be executed while init and write new config
var loadConfigFns = []struct {
	step string
	fn   func() error
}{{"load_config", func() error {
	f, err := os.OpenFile(Conf.file, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewDecoder(f).Decode(&Conf)
}}}

func PersistRule(domain string) {
	Client.Router.DynamicList = append(Client.Router.DynamicList, domain)
	select {
	case flushCh <- struct{}{}:
	default:
	}
}
func flushConfDaemon() {
	for range flushCh {
		// safe write file
		if Conf.file != "" {
			f, err := os.OpenFile(Conf.file+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}

			if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(&Conf); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				f.Close()
				continue
			}
			f.Close()

			if err = os.Rename(Conf.file+"~", Conf.file); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}
		}

		// reload config
		for i := range loadConfigFns {
			if err := loadConfigFns[i].fn(); err != nil {
				log.Errorw("flush config", "step", loadConfigFns[i].step, "err", err)
			}
		}
	}
}
