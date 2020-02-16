package util

import (
	"net"
	"strconv"
)

func ParseHostPort(addr string, defaultPort uint16) (string, uint16) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		if defaultPort == 0 {
			panic("parse port fail with no default, addr: " + addr)
		}
		return addr, defaultPort
	}

	pNum, _ := strconv.ParseUint(p, 10, 16)
	return h, uint16(pNum)
}
