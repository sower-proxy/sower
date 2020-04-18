package transport

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"

	"github.com/wweir/sower/dhcp"
	"github.com/wweir/utils/log"
)

var (
	persistDNS string
	dnsAddr    string
	resolver   = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dnsAddr)
		},
	}
)

func SetDNS(err error, dnsIP string) {
	if dnsIP != "" {
		persistDNS = dnsIP
		dnsAddr = net.JoinHostPort(dnsIP, "53")
		return
	} else if persistDNS != "" {
		return
	}

	if e, ok := err.(*net.DNSError); !ok /*nil*/ || !e.IsNotFound {
		if dnsIP, err = dhcp.GetDefaultDNSServer(); err != nil {
			dnsIP, err = dhcp.GetDefaultDNSServer() // retry
		}
		if err != nil {
			log.Errorw("get dns via dhcp", "err", err, "current_dns", dnsAddr)
		} else {
			dnsAddr = net.JoinHostPort(dnsIP, "53")
		}
	}
}

// Dial dial targetAddr with possiable proxy address
func Dial(targetAddr string, dialAddr func(domain string) (proxyAddr string, password []byte)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}

	address, password := dialAddr(host)
	if address == "" {
		ips, err := resolver.LookupIPAddr(context.Background(), host)
		if err != nil { //retry
			ips, err = resolver.LookupIPAddr(context.Background(), host)
		}
		if err != nil {
			SetDNS(err, "")
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

	conn, err := tls.DialWithDialer(&net.Dialer{Resolver: resolver},
		"tcp", net.JoinHostPort(address, "443"), &tls.Config{})
	if err != nil {
		return nil, err
	}
	return ToProxyConn(conn, host, uint16(p), password)
}
