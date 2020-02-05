package conf

import (
	"flag"
	"os"
	"sync"

	toml "github.com/pelletier/go-toml"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
)

var (
	version, date string

	flushOnce = sync.Once{}
	flushMu   = sync.Mutex{}
	flushCh   = make(chan struct{})

	// Conf define the config items
	Conf = struct {
		ConfigFile string

		Upstream struct {
			Socks5 string `toml:"socks5"`
			DNS    string `toml:"dns"`
		} `toml:"upstream"`

		Downstream struct {
			ServeIP   string `toml:"serve_ip"`
			HTTPProxy string `toml:"http_proxy"`
		} `toml:"downstream"`

		Router struct {
			FlushDNSCmd string `toml:"flush_dns_cmd"`
			ProxyLevel  int    `toml:"proxy_level"`

			PortMapping map[string]string `toml:"port_mapping"`
			ProxyList   []string          `toml:"proxy_list"`
			DirectList  []string          `toml:"direct_list"`
			DynamicList []string          `toml:"dynamic_list"`
		} `toml:"router"`
	}{}
)

func init() {
	flag.StringVar(&Conf.ConfigFile, "f", "", "config file, keep empty for dynamic detect proxy rule")
	flag.StringVar(&Conf.Upstream.Socks5, "socks5", "127.0.0.1:1080", "upstream socks5 address")
	flag.StringVar(&Conf.Upstream.DNS, "dns", "", "upstream dns ip, keep empty to dynamic detect")
	flag.StringVar(&Conf.Downstream.ServeIP, "serve", "127.0.0.1", "serve on address")
	flag.StringVar(&Conf.Downstream.HTTPProxy, "http_proxy", "", "serve http proxy, eg: 127.0.0.1:8080")
	flag.IntVar(&Conf.Router.ProxyLevel, "level", 2, "dynamic proxy level: 0~4")

	Init() // execute platform init logic
	if !flag.Parsed() {
		flag.Parse()
	}

	if _, err := os.Stat(Conf.ConfigFile); err == nil {
		for i := range loadConfigFns {
			if err := loadConfigFns[i].fn(); err != nil {
				log.Fatalw("load config", "config", Conf.ConfigFile, "step", loadConfigFns[i].step, "err", err)
			}
		}
	}

	log.Infow("start", "version", version, "date", date, "config", Conf)
}

// refreshFns will be executed while init and write new config
var loadConfigFns = []struct {
	step string
	fn   func() error
}{{"load_config", func() error {
	f, err := os.OpenFile(Conf.ConfigFile, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	//safe refresh config
	file := Conf.ConfigFile
	if err = toml.NewDecoder(f).Decode(&Conf); err != nil {
		return err
	}
	Conf.ConfigFile = file
	return nil

}}, {"flush_dns", func() error {
	if Conf.Router.FlushDNSCmd != "" {
		return execute(Conf.Router.FlushDNSCmd)
	}
	return nil
}}}

// AddReloadConfigHook add hook function for reload config
func AddReloadConfigHook(step string, fn func() error) {
	loadConfigFns = append(loadConfigFns, struct {
		step string
		fn   func() error
	}{step, fn})
}

// AddDynamic add new domain into dynamic list
func AddDynamic(domain string) {
	flushMu.Lock()
	Conf.Router.DynamicList = append(Conf.Router.DynamicList, domain)
	Conf.Router.DynamicList = util.NewReverseSecSlice(Conf.Router.DynamicList).Sort().Uniq()
	flushMu.Unlock()

	flushOnce.Do(func() {
		if Conf.ConfigFile != "" {
			go flushConf()
		}
	})

	select {
	case flushCh <- struct{}{}:
	default:
	}
}

func flushConf() {
	for range flushCh {
		// safe write
		if Conf.ConfigFile != "" {
			f, err := os.OpenFile(Conf.ConfigFile+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				continue
			}

			flushMu.Lock()
			if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(Conf); err != nil {
				log.Errorw("flush config", "step", "flush", "err", err)
				flushMu.Unlock()
				f.Close()
				continue
			}
			flushMu.Unlock()
			f.Close()

			if err = os.Rename(Conf.ConfigFile+"~", Conf.ConfigFile); err != nil {
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
