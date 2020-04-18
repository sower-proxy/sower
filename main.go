package main

import (
	"flag"
	"fmt"

	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/proxy"
	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
)

func main() {
	client, server, password := conf.Init()

	switch {
	case server.Upstream != "":
		proxy.StartServer(server.Upstream, password, conf.ConfigDir,
			server.CertFile, server.KeyFile, server.CertEmail)

	case client.Address != "":
		route := &router.Route{
			ProxyAddress:  client.Address,
			ProxyPassword: password,
			DetectLevel:   client.Router.DetectLevel,
			DirectList:    client.Router.DirectList,
			ProxyList:     client.Router.ProxyList,
			PersistFn:     conf.PersistRule,
		}

		if client.HTTPProxy != "" {
			go proxy.StartHTTPProxy(client.HTTPProxy, client.Address,
				[]byte(password), route.GenProxyCheck(true))
		}

		enableDNSSolution := client.DNSServeIP != ""
		if enableDNSSolution {
			transport.SetDNS(nil, client.DNSUpstream)
			go proxy.StartDNS(client.DNSServeIP, client.DNSUpstream,
				route.GenProxyCheck(false))
		}

		proxy.StartClient(client.Address, password, enableDNSSolution,
			client.PortForward, route.GenProxyCheck(true))

	default:
		fmt.Println()
		flag.Usage()
	}
}
