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
	"github.com/wweir/sower/util"
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
	initArgs()

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

				glog.V(1).Infof("watch %s event: %v", Conf.ConfigFile, event)
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
	Conf.Suggestions = util.NewReverseSecSlice(Conf.Suggestions).Sort().Uniq()

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
