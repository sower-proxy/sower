//go:build !windows
// +build !windows

package dhcp

import (
	"errors"
	"net"
)

// PickInternetInterface pick the first active net interface
func PickInternetInterface() (*Iface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(ifaces[i].HardwareAddr) == 0 {
			continue
		}

		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil && ip.IsGlobalUnicast() {
				return &Iface{ifaces[i].HardwareAddr, ip}, nil
			}
		}
	}
	return nil, errors.New("no valid interface")
}
