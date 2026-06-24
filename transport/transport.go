package transport

import (
	"net"
)

// Transport wraps and unwraps a proxy protocol frame on a net.Conn.
// Wrap writes the protocol header (including target address) to the connection.
// Unwrap reads and validates the protocol header, returning the target address.
// Neither method closes the connection; the caller is responsible for that.
type Transport interface {
	Unwrap(conn net.Conn) (net.Addr, error)
	Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error
}
