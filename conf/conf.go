package conf

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/golang/glog"
	toml "github.com/pelletier/go-toml"
	"github.com/wweir/sower/util"
)

// Conf define the config items
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

// OnRefreash will be executed while init and write new config
var OnRefreash = []func() error{
	func() (err error) {
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

		return flag.Set("v", strconv.Itoa(Conf.Verbose))
	},
	func() error {
		if Conf.ClearDNSCache != "" {
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
			defer cancel()

			switch runtime.GOOS {
			case "windows":
				return exec.CommandContext(ctx, "cmd", "/c", Conf.ClearDNSCache).Run()
			default:
				return exec.CommandContext(ctx, "sh", "-c", Conf.ClearDNSCache).Run()
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
}

// mu keep synchronized add rule(write), do not care read while write
var mu = &sync.Mutex{}

// AddSuggestion add new domain into suggest rules
func AddSuggestion(domain string) {
	mu.Lock()
	defer mu.Unlock()

	Conf.Suggestions = append(Conf.Suggestions, domain)
	Conf.Suggestions = util.NewReverseSecSlice(Conf.Suggestions).Sort().Uniq()

	{ // safe write
		f, err := os.OpenFile(Conf.ConfigFile+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			glog.Errorln(err)
			return
		}

		if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(Conf); err != nil {
			glog.Errorln(err)
			f.Close()
			return
		}
		f.Close()

		if err = os.Rename(Conf.ConfigFile+"~", Conf.ConfigFile); err != nil {
			glog.Errorln(err)
			return
		}
	}

	// reload config
	for i := range OnRefreash {
		if err := OnRefreash[i](); err != nil {
			glog.Errorln(err)
		}
	}
}
