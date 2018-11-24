package main

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
)

func main() {
	glog.Infoln("Starting:", conf.Conf)

	if conf.Conf.ServerAddr == "" {
		proxy.StartServer(conf.Conf.ServerPort)
	} else {
		go dns.StartDNS(conf.Conf.DnsServer, conf.Conf.BlockList)
		proxy.StartClient(conf.Conf.ServerAddr)
	}
}
