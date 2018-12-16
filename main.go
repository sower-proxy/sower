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
		proxy.StartServer(conf.Conf.NetType, conf.Conf.ServerPort)
	} else {
		go dns.StartDNS(conf.Conf.DnsServer)
		proxy.StartClient(conf.Conf.NetType, conf.Conf.ServerAddr)
	}
}
