package conf

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/golang/glog"
	toml "github.com/pelletier/go-toml"
	"github.com/wweir/fsnotify"
)

var Conf = struct {
	ConfigFile string
	NetType    string `toml:"net_type"`
	Cipher     string `toml:"cipher"`
	Password   string `toml:"password"`

	ServerPort string `toml:"server_port"`
	ServerAddr string `toml:"server_addr"`
	HTTPProxy  string `toml:"http_proxy"`

	DNSServer     string `toml:"dns_server"`
	ClientIP      string `toml:"client_ip"`
	ClearDNSCache string `toml:"clear_dns_cache"`

	BlockList   []string `toml:"blocklist"`
	WhiteList   []string `toml:"whitelist"`
	Suggestions []string `toml:"suggestions"`
	Verbose     int      `toml:"verbose"`
}{}
var mu = &sync.Mutex{}

var OnRefreash = []func() error{
	func() (err error) {
		mu.Lock()
		defer mu.Unlock()

		f, err := os.OpenFile(Conf.ConfigFile, os.O_RDONLY, 0644)
		if err != nil {
			return err
		}
		defer f.Close()

		file := Conf.ConfigFile
		if err = toml.NewDecoder(f).Decode(&Conf); err != nil {
			return err
		}
		Conf.ConfigFile = file

		return flag.Set("v", strconv.Itoa(Conf.Verbose))
	},
	func() error {
		if Conf.ClearDNSCache != "" {
			ctx, _ := context.WithTimeout(context.TODO(), 5*time.Second)
			if err := exec.CommandContext(ctx, "sh", "-c", Conf.ClearDNSCache).Run(); err != nil {
				glog.Errorln(err)
			}
		}
		return nil
	},
}

func init() {
	flag.StringVar(&Conf.ConfigFile, "f", filepath.Dir(os.Args[0])+"/sower.toml", "config file location")
	flag.StringVar(&Conf.NetType, "n", "TCP", "proxy net type (QUIC|KCP|TCP)")
	flag.StringVar(&Conf.Cipher, "C", "AES_128_GCM", "cipher type (AES_128_GCM|AES_192_GCM|AES_256_GCM|CHACHA20_IETF_POLY1305|XCHACHA20_IETF_POLY1305)")
	flag.StringVar(&Conf.Password, "p", "12345678", "password")
	flag.StringVar(&Conf.ServerPort, "P", "5533", "server mode listen port")
	flag.StringVar(&Conf.ServerAddr, "s", "", "server IP (run in client mode if set)")
	flag.StringVar(&Conf.HTTPProxy, "H", "", "http proxy listen addr")
	flag.StringVar(&Conf.DNSServer, "d", "114.114.114.114", "client dns server")
	flag.StringVar(&Conf.ClientIP, "c", "127.0.0.1", "client dns service redirect IP")

	if !flag.Parsed() {
		flag.Set("logtostderr", "true")
		flag.Parse()
	}

	if _, err := os.Stat(Conf.ConfigFile); os.IsNotExist(err) {
		glog.Warningln("no config file has been load:", Conf.ConfigFile)
		return
	}
	for i := range OnRefreash {
		if err := OnRefreash[i](); err != nil {
			glog.Fatalln(err)
		}
	}
	watchConfigFile()
}

func watchConfigFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		glog.Fatalln(err)
	}
	if err := watcher.Add(filepath.Dir(Conf.ConfigFile)); err != nil {
		glog.Fatalln(err)
	}

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op == fsnotify.Rename {
					if err := watcher.Add(Conf.ConfigFile); err != nil {
						glog.Errorln(err)
					}
				}

				glog.Infof("watch %s event: %v", Conf.ConfigFile, event)
				for i := range OnRefreash {
					if err := OnRefreash[i](); err != nil {
						glog.Errorln(err)
					}
				}
			case err := <-watcher.Errors:
				glog.Fatalln(err)
			}
		}
	}()
}

func AddSuggest(domain string) {
	mu.Lock()
	defer mu.Unlock()

	Conf.Suggestions = append(Conf.Suggestions, domain)

	// safe write
	f, err := os.OpenFile(Conf.ConfigFile+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		glog.Errorln(err)
		return
	}
	defer f.Close()

	if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(Conf); err != nil {
		glog.Errorln(err)
		return
	}

	if err = os.Rename(Conf.ConfigFile+"~", Conf.ConfigFile); err != nil {
		glog.Errorln(err)
	}
}
