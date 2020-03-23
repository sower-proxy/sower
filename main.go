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
	case conf.Conf.Server.Upstream != "":
		proxy.StartServer(conf.Conf.Server.Upstream, conf.Conf.Password, conf.ConfigDir,
			conf.Conf.Server.CertFile, conf.Conf.Server.KeyFile, conf.Conf.Server.CertEmail)

	case conf.Conf.Client.Address != "":
		route := &router.Route{
			ProxyAddress:  conf.Conf.Client.Address,
			ProxyPassword: conf.Conf.Password,
			DetectLevel:   conf.Conf.Client.Router.DetectLevel,
			DetectTimeout: conf.Conf.Client.Router.DetectTimeout,
			DirectList:    conf.Conf.Client.Router.DirectList,
			ProxyList:     conf.Conf.Client.Router.ProxyList,
			DynamicList:   conf.Conf.Client.Router.DynamicList,
			PersistFn:     conf.PersistRule,
		}

		go proxy.StartHTTPProxy(conf.Conf.Client.HTTPProxy, conf.Conf.Client.Address,
			[]byte(conf.Conf.Password), route.ShouldProxy)

		enableDNSSolution := conf.Conf.Client.DNSServeIP != ""
		if enableDNSSolution {
			go proxy.StartDNS(conf.Conf.Client.DNSServeIP, conf.Conf.Client.DNSUpstream,
				route.ShouldProxy)
		}

		proxy.StartClient(conf.Conf.Client.Address, conf.Conf.Password, enableDNSSolution,
			conf.Conf.Client.PortForward, route.ShouldProxy)

	default:
		fmt.Println()
		flag.Usage()
	}
}
