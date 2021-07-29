package transport

import (
	"net"
)

type Transport interface {
	Unwrap(conn net.Conn) (net.Addr, error)
	Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error
}
