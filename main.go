package main

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
)

func main() {
	conf := conf.Conf
	glog.Infoln("Starting:", conf)

	if conf.ServerAddr == "" {
		proxy.StartServer(conf.NetType, conf.ServerPort, conf.Cipher, conf.Password)
	} else {
		go dns.StartDNS(conf.DnsServer, conf.ClientIP, conf.ClientIPNet)
		proxy.StartClient(conf.NetType, conf.ServerAddr, conf.Cipher, conf.Password, conf.ClientIP)
	}
}
