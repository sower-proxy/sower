package transport

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"

	"github.com/wweir/sower/dhcp"
	"github.com/wweir/sower/util"
)

var (
	dnsAddr string
	preSet  bool
)

func SetDNS(dnsIP string) {
	dnsAddr = net.JoinHostPort(dnsIP, "53")
	preSet = true
}

// Dial dial targetAddr with possiable proxy address
func Dial(targetAddr string, dialAddr func(domain string) (proxyAddr string, password []byte)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}

	address, password := dialAddr(host)
	if address == "" {
		ips, err := (&net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, dnsAddr)
			},
		}).LookupIPAddr(context.Background(), host)
		if err != nil {
			if !preSet {
				if e, ok := err.(*net.DNSError); !ok || !e.IsNotFound {
					if ip, err := dhcp.GetDefaultDNSServer(); err == nil {
						dnsAddr = net.JoinHostPort(ip, "53")
					}
				}
			}

			return nil, err
		}

		return net.Dial("tcp", net.JoinHostPort(ips[0].String(), port))
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}

	if addr, ok := IsSocks5Schema(address); ok {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if conn, err = ToSocks5(conn, host, uint16(p)); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}

	address, _ = util.WithDefaultPort(address, "443")
	// tls.Config is same as golang http pkg default behavior
	conn, err := tls.Dial("tcp", address, &tls.Config{})
	if err != nil {
		return nil, err
	}
	return ToProxyConn(conn, host, uint16(p), &tls.Config{}, password)
}
