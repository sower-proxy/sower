package main

import (
	"flag"
	"fmt"

	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/proxy"
)

func main() {
	if conf.Server.Upstream != "" {
		proxy.StartServer(conf.Server.Upstream, conf.Password,
			conf.Server.CertFile, conf.Server.KeyFile, conf.Server.CertEmail)
	}

	if conf.Client.Address != "" {
		if conf.Client.DNS.ServeIP != "" {
			go proxy.StartDNS(conf.Client.DNS.ServeIP, conf.Client.DNS.Upstream)
		}

		proxy.StartClient(conf.Password, conf.Client.Address, conf.Client.HTTPProxy.Address,
			conf.Client.DNS.ServeIP, conf.Client.Router.PortMapping)
	}

	if conf.Server.Upstream == "" && conf.Client.Address == "" {
		fmt.Println()
		flag.Usage()
	}
}
