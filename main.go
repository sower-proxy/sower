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
		route := router.NewRoute(client.Address, password, client.Router.DetectLevel,
			client.Router.BlockList, client.Router.ProxyList, client.Router.DirectList,
			conf.PersistRule)

		if client.Socks5Proxy != "" {
			go proxy.StartSocks5Proxy(client.Socks5Proxy, client.Address, []byte(password))
		}

		if client.HTTPProxy != "" {
			go proxy.StartHTTPProxy(client.HTTPProxy, client.Address,
				[]byte(password), route.GenProxyCheck(true))
		}

		transport.SetDNS(nil, client.DNSUpstream)
		go proxy.StartDNS(client.DNSUpstream, route.GenProxyCheck(false))

		proxy.StartClient(client.Address, password,
			client.PortForward, route.GenProxyCheck(true))

	default:
		fmt.Println()
		flag.Usage()
	}
}
