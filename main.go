package main

import (
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy"
)

func main() {
	if conf.Server.Relay != "" {
		proxy.StartServer(conf.Server.Relay, conf.Password,
			conf.Server.CertFile, conf.Server.KeyFile, conf.Server.CertEmail)
	}

	if conf.Client.Address != "" {
		if conf.Client.DNS.RedirectIP != "" {
			go dns.ServeDNS(conf.Client.DNS.RedirectIP, conf.Server.Relay)
		}

		proxy.StartClient(conf.Password, conf.Client.Address, conf.Client.HTTPProxy.Address,
			conf.Client.DNS.RedirectIP, conf.Client.Router.PortMapping)
	}
}
