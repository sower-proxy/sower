package router

import (
	"net"
	"strconv"
)

func ParseHostPort(hostport string, defaultPort uint16) (string, uint16, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, err
	}
	if port == "" {
		return host, defaultPort, nil
	}

	portInt, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", 0, err
	}
	return host, uint16(portInt), nil
}
