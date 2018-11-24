package main

import (
	"log"

	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
func main() {
	log.Println(conf.Conf)

	if conf.Conf.ServerAddr == "" {
		proxy.StartServer(conf.Conf.ServerPort)
	} else {
		dns.StartDNS(conf.Conf.DnsServer, conf.Conf.BlockList)
		proxy.StartClient(conf.Conf.ServerAddr)
	}
}
