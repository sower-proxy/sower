package router

import (
	"net"
	"net/url"
	"strconv"
)

func ParseHostPort(hostport string, u *url.URL) (string, uint16, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		if err.(*net.AddrError).Err == "missing port in address" {
			switch u.Scheme {
			case "http":
				return hostport, 80, nil
			case "https":
				return hostport, 443, nil
			}
		}
		return "", 0, err
	}

	portInt, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(portInt), nil
}
