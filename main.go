package main

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
)

var version, date string

func main() {
	conf := conf.Conf
	glog.Infof("Starting sower(%s %s): %v", version, date, conf)

	if conf.ServerAddr == "" {
		proxy.StartServer(conf.NetType, conf.ServerPort, conf.Cipher, conf.Password)

	} else {
		if conf.HTTPProxy != "" {
			go proxy.StartHttpProxy(conf.NetType, conf.ServerAddr,
				conf.Cipher, conf.Password, conf.HTTPProxy)
		}

		go dns.StartDNS(conf.DNSServer, conf.ClientIP)
		proxy.StartClient(conf.NetType, conf.ServerAddr, conf.Cipher, conf.Password, conf.ClientIP)
	}
}
