package main

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
	"github.com/wweir/sower/proxy/transport"
)

var version, date string

func main() {
	conf := conf.Conf
	glog.Infof("Starting sower(%s %s): %v", version, date, conf)

	tran, err := transport.GetTransport(conf.NetType)
	if err != nil {
		glog.Exitln(err)
	}

	if conf.ServerAddr == "" {
		proxy.StartServer(tran, conf.ServerPort, conf.Cipher, conf.Password)

	} else {
		if conf.HTTPProxy != "" {
			go proxy.StartHttpProxy(tran, conf.ServerAddr,
				conf.Cipher, conf.Password, conf.HTTPProxy)
		}

		go dns.StartDNS(conf.DNSServer, conf.ClientIP)
		proxy.StartClient(tran, conf.ServerAddr, conf.Cipher, conf.Password, conf.ClientIP)
	}
}
