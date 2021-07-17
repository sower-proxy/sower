package transport

import (
	"net"

	"github.com/wweir/sower/pkg/teeconn"
)

type Transport interface {
	Unwrap(conn *teeconn.Conn) net.Addr
	Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error
}
