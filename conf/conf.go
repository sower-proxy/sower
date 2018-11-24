package conf

import (
	"flag"

	"github.com/BurntSushi/toml"
)

var Conf = struct {
	ConfigFile string
	ServerPort string   `toml:"server_port"`
	ServerAddr string   `toml:"server_addr"`
	DnsServer  string   `toml:"dns_server"`
	BlockList  []string `toml:"blocklist"`
	Debug      bool     `toml:"debug"`
}{}

func init() {
	flag.StringVar(&Conf.ConfigFile, "f", "", "config file location")
	flag.StringVar(&Conf.ServerPort, "P", "5533", "server mode listen port")
	flag.StringVar(&Conf.ServerAddr, "s", "", "server addr (run in client mode if set)")
	flag.StringVar(&Conf.DnsServer, "d", "114.114.114.114", "client dns server")
	flag.BoolVar(&Conf.Debug, "D", false, "run in debug mode")

	if !flag.Parsed() {
		flag.Parse()
	}

	if Conf.ConfigFile == "" {
		return
	}
	if _, err := toml.DecodeFile(Conf.ConfigFile, &Conf); err != nil {
		panic(err)
	}
}
