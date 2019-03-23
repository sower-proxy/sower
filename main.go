package main

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
	"github.com/wweir/sower/proxy/transport"
)

var version, date string

func main() {
	cfg := &conf.Conf
	glog.Infof("Starting sower(%s %s): %v", version, date, cfg)

	tran, err := transport.GetTransport(cfg.NetType)
	if err != nil {
		glog.Exitln(err)
	}

	if cfg.ServerAddr == "" {
		proxy.StartServer(tran, cfg.ServerPort, cfg.Cipher, cfg.Password)

	} else {
		conf.AddRefreshFn(true, func() (string, error) {
			host, _, _ := net.SplitHostPort(cfg.ServerAddr)
			dns.LoadRules(cfg.BlockList, cfg.Suggestions, cfg.WhiteList, host)
			return "load rules", nil
		})

		isSocks5 := (cfg.NetType == "SOCKS5")

		if cfg.HTTPProxy != "" {
			go proxy.StartHttpProxy(tran, isSocks5, cfg.ServerAddr, cfg.Cipher, cfg.Password, cfg.HTTPProxy)
		}

		go dns.StartDNS(cfg.DNSServer, cfg.ClientIP, conf.SuggestCh, cfg.SuggestLevel)
		proxy.StartClient(tran, isSocks5, cfg.ServerAddr, cfg.Cipher, cfg.Password, cfg.ClientIP)
	}
}
