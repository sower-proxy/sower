package conf

import (
	"flag"
	"net"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/wweir/fsnotify"
	"github.com/golang/glog"
)

var Conf = struct {
	ConfigFile  string
	ServerPort  string   `toml:"server_port"`
	ServerAddr  string   `toml:"server_addr"`
	DnsServer   string   `toml:"dns_server"`
	ClientIP    string   `toml:"client_ip"`
	ClientIPNet net.IP   `toml:"-"`
	BlockList   []string `toml:"blocklist"`
	Verbose     int      `toml:"verbose"`
}{}

func init() {
	flag.StringVar(&Conf.ConfigFile, "f", "", "config file location")
	flag.StringVar(&Conf.ServerPort, "P", "5533", "server mode listen port")
	flag.StringVar(&Conf.ServerAddr, "s", "", "server IP (run in client mode if set)")
	flag.StringVar(&Conf.DnsServer, "d", "114.114.114.114", "client dns server")
	flag.StringVar(&Conf.ClientIP, "c", "127.0.0.1", "client dns service redirect IP")

	if !flag.Parsed() {
		flag.Parse()
	}

	if Conf.ConfigFile == "" {
		return
	}
	if err := OnRefreash[0](); err != nil {
		panic(err)
	}
	watchConfigFile()
}

var OnRefreash = []func() error{func() error {
	if _, err := toml.DecodeFile(Conf.ConfigFile, &Conf); err != nil {
		return err
	}
	Conf.ClientIPNet = net.ParseIP(Conf.ClientIP)
	// for glog
	if err := flag.Set("v", strconv.Itoa(Conf.Verbose)); err != nil {
		return err
	}
	return nil
}}

// watchConfigFile changes, fsnotify take too much cpu time, DIY
func watchConfigFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		glog.Fatalln(err)
	}
	if err := watcher.Add(Conf.ConfigFile); err != nil {
		glog.Fatalln(err)
	}

	go func() {
		for {
			select {
			case event := <-watcher.Events:
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
