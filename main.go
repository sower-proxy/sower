package main

import (
	"flag"
	"fmt"

	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/proxy"
	"github.com/wweir/sower/router"
)

func main() {
	switch {
	case conf.Server.Upstream != "":
		proxy.StartServer(conf.Server.Upstream, conf.Conf.Password,
			conf.Server.CertFile, conf.Server.KeyFile, conf.Server.CertEmail)

	case conf.Client.Address != "":
		route := &router.Route{
			ProxyAddress:  conf.Client.Address,
			ProxyPassword: conf.Conf.Password,
			DetectLevel:   conf.Client.Router.DetectLevel,
			DetectTimeout: conf.Client.Router.DetectTimeout,
			DirectList:    conf.Client.Router.DirectList,
			ProxyList:     conf.Client.Router.ProxyList,
			DynamicList:   conf.Client.Router.DynamicList,
		}

		go proxy.StartHTTPProxy(conf.Client.HTTPProxy, conf.Client.Address,
			[]byte(conf.Conf.Password), route.ShouldProxy)

		if conf.Client.DNSServeIP != "" {
			go proxy.StartDNS(conf.Client.DNSServeIP, conf.Client.DNSUpstream,
				route.ShouldProxy)
		}

		proxy.StartClient(conf.Client.Address, conf.Conf.Password,
			conf.Client.DNSServeIP != "", conf.Client.PortForward)

	default:
		if conf.Server.Upstream == "" && conf.Client.Address == "" {
			fmt.Println()
			flag.Usage()
		}
	}
}
