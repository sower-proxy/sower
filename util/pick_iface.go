package util

import (
	"net"
)

// Iface is net interface address info
type Iface struct {
	net.HardwareAddr
	net.IP
}
