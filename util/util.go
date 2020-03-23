package util

import "net"

func WithDefaultPort(addr string, port string) (address, host string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return net.JoinHostPort(addr, port), addr
	}
	return addr, host
}
