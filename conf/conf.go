package conf

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/pkg/errors"

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
	SuggestLevel  string `toml:"suggest_level"`
	ClearDNSCache string `toml:"clear_dns_cache"`

	BlockList   []string `toml:"blocklist"`
	WhiteList   []string `toml:"whitelist"`
	Suggestions []string `toml:"suggestions"`
	Verbose     int      `toml:"verbose"`
	VersionOnly bool     `toml:"-"`
}{}

func init() {
	initArgs()

	if _, err := os.Stat(Conf.ConfigFile); os.IsNotExist(err) {
		glog.Warningln("no config file has been load:", Conf.ConfigFile)
		return
	}
	for i := range refreshFns {
		if action, err := refreshFns[i](); err != nil {
			glog.Fatalln(action+":", err)
		}
	}

	go addSuggestions()
}

// refreshFns will be executed while init and write new config
var refreshFns = []func() (string, error){
	func() (string, error) {
		action := "load config"
		f, err := os.OpenFile(Conf.ConfigFile, os.O_RDONLY, 0644)
		if err != nil {
			return action, err
		}
		defer f.Close()

		//safe refresh config
		file := Conf.ConfigFile
		if err = toml.NewDecoder(f).Decode(&Conf); err != nil {
			return action, err
		}
		Conf.ConfigFile = file

		return action, flag.Set("v", strconv.Itoa(Conf.Verbose))
	},
	func() (string, error) {
		action := "clear dns cache"
		if Conf.ClearDNSCache != "" {
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
			defer cancel()

			switch runtime.GOOS {
			case "windows":
				if out, err := exec.CommandContext(ctx, "cmd", "/c", Conf.ClearDNSCache).CombinedOutput(); err != nil {
					return action, errors.Wrapf(err, "cmd: %s, output: %s, error", Conf.ClearDNSCache, out)
				}
			default:
				if out, err := exec.CommandContext(ctx, "sh", "-c", Conf.ClearDNSCache).CombinedOutput(); err != nil {
					return action, errors.Wrapf(err, "cmd: %s, output: %s, error", Conf.ClearDNSCache, out)
				}
			}
		}
		return action, nil
	},
}

// AddRefreshFn add refreshh function for reload config
func AddRefreshFn(init bool, fn func() (string, error)) error {
	if init {
		if _, err := fn(); err != nil {
			return err
		}
	}

	refreshFns = append(refreshFns, fn)
	return nil
}

// SuggestCh add domain into suggestios
var SuggestCh = make(chan string)

// addSuggestions add new domain into suggest rules
func addSuggestions() {
	for domain := range SuggestCh {
		Conf.Suggestions = append(Conf.Suggestions, domain)
		Conf.Suggestions = util.NewReverseSecSlice(Conf.Suggestions).Sort().Uniq()

		{ // safe write
			f, err := os.OpenFile(Conf.ConfigFile+"~", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
			if err != nil {
				glog.Errorln(err)
				continue
			}

			if err := toml.NewEncoder(f).ArraysWithOneElementPerLine(true).Encode(Conf); err != nil {
				glog.Errorln(err)
				f.Close()
				continue
			}
			f.Close()

			if err = os.Rename(Conf.ConfigFile+"~", Conf.ConfigFile); err != nil {
				glog.Errorln(err)
				continue
			}
		}

		// reload config
		for i := range refreshFns {
			if action, err := refreshFns[i](); err != nil {
				glog.Errorln(action+":", err)
			}
		}
	}
}
