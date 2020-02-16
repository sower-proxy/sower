package conf

import (
	"flag"
	"os"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
)

type client struct {
	Address string `toml:"address"`

	HTTPProxy struct {
		Address string `toml:"address"`
	} `toml:"http_proxy"`

	DNS struct {
		ServeIP  string `toml:"serve_ip"`
		Upstream string `toml:"upstream"`
		FlushCmd string `toml:"flush_cmd"`
	} `toml:"dns"`

	Router struct {
		PortMapping   map[string]string `toml:"port_mapping"`
		DetectLevel   int               `toml:"detect_level"`
		DetectTimeout string            `toml:"detect_timeout"`

		ProxyList    []string `toml:"proxy_list"`
		DirectList   []string `toml:"direct_list"`
		DynamicList  []string `toml:"dynamic_list"`
		directRules  *util.Node
		proxyRules   *util.Node
		dynamicRules *util.Node
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

	flushOnce = sync.Once{}
	flushMu   = sync.Mutex{}
	flushCh   = make(chan struct{})

	Server = server{}
	Client = client{}
	conf   = struct {
		file   string
		Server *server `toml:"server"`
		Client *client `toml:"client"`
	}{"", &Server, &Client}
	Password      string
	installCmd    string
	uninstallFlag bool
)

func init() {
	flag.StringVar(&Password, "password", "", "password")
	flag.StringVar(&Server.Upstream, "s", "", "upstream http service, eg: 127.0.0.1:8080")
	flag.StringVar(&Server.CertFile, "s_cert", "", "tls cert file, gen cert from letsencrypt if empty")
	flag.StringVar(&Server.KeyFile, "s_key", "", "tls key file, gen cert from letsencrypt if empty")
	flag.StringVar(&Client.Address, "c", "", "remote server domain, eg: aa.bb.cc, socks5h://127.0.0.1:1080")
	flag.StringVar(&Client.HTTPProxy.Address, "http_proxy", ":8080", "http proxy, empty to disable")
	flag.StringVar(&Client.DNS.ServeIP, "dns_ip", "", "upstream dns, eg: 127.0.0.1, disable dns proxy if empty")
	flag.StringVar(&Client.DNS.Upstream, "dns_upstream", "", "dns relay server ip, dynamic detect if empty")
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

	var err error
	defer func() {
		if timeout, err = time.ParseDuration(Client.Router.DetectTimeout); err != nil {
			log.Fatalw("parse dynamic detect timeout", "val", Client.Router.DetectTimeout, "err", err)
		}

		log.Infow("start", "version", version, "date", date, "conf", &conf)
		passwordData = []byte(Password)
	}()

	if conf.file == "" {
		return
	}

	for i := range loadConfigFns {
		if err = loadConfigFns[i].fn(); err != nil {
			log.Fatalw("load config", "config", conf.file, "step", loadConfigFns[i].step, "err", err)
		}
	}
}

// refreshFns will be executed while init and write new config
var loadConfigFns = []struct {
	step string
	fn   func() error
}{{"load_config", func() error {
	f, err := os.OpenFile(conf.file, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewDecoder(f).Decode(&conf)

}}, {"load_rules", func() error {
	Client.Router.directRules = util.NewNodeFromRules(Client.Router.DirectList...)
	Client.Router.proxyRules = util.NewNodeFromRules(Client.Router.ProxyList...)
	Client.Router.dynamicRules = util.NewNodeFromRules(Client.Router.DynamicList...)
	return nil

}}, {"flush_dns", func() error {
	if Client.DNS.FlushCmd != "" {
		return execute(Client.DNS.FlushCmd)
	}
	return nil
}}}

func flushConf() {
	for range flushCh {
		// safe write
		if conf.file != "" {
			f, err := os.OpenFile(conf.file+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}

			flushMu.Lock()
			if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(conf); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				flushMu.Unlock()
				f.Close()
				continue
			}
			flushMu.Unlock()
			f.Close()

			if err = os.Rename(conf.file+"~", conf.file); err != nil {
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
