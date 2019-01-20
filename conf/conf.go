package conf

import (
	"context"
	"flag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/golang/glog"
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
	ClientIPNet   net.IP `toml:"-"`
	ClearDNSCache string `toml:"clear_dns_cache"`

	BlockList   []string `toml:"blocklist"`
	Suggestions []string `toml:"suggestions"`
	Verbose     int      `toml:"verbose"`
}{}

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

	// clear dns cache
	if Conf.ClearDNSCache != "" {
		ctx, _ := context.WithTimeout(context.TODO(), 5*time.Second)
		if err := exec.CommandContext(ctx, "sh", "-c", Conf.ClearDNSCache).Run(); err != nil {
			glog.Errorln(err)
		}
	}

	// for glog
	if err := flag.Set("v", strconv.Itoa(Conf.Verbose)); err != nil {
		return err
	}
	return nil
}}

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
