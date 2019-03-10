// +build !windows

package util

import (
	"errors"
	"net"
)

// PickInterface pick the first active net interface
func PickInterface() (*Iface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for i := range ifaces {
		if len(ifaces[i].HardwareAddr) == 0 {
			continue
		}

		addrs, _ := ifaces[i].Addrs()
		for _, addr := range addrs {
			if ip := addr.(*net.IPNet).IP.To4(); ip != nil {
				return &Iface{ifaces[i].HardwareAddr, ip}, nil
			}
		}
	}
	return nil, errors.New("no valid interface")
}
