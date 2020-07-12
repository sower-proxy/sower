package transport

import (
	"encoding/binary"
	"net"
	"strings"

	"golang.org/x/xerrors"
)

func IsSocks5Schema(addr string) (string, bool) {
	if strings.HasPrefix(addr, "socks5://") {
		return strings.TrimPrefix(addr, "socks5://"), true
	}

	if strings.HasPrefix(addr, "socks5h://") {
		return strings.TrimPrefix(addr, "socks5h://"), true
	}

	return addr, false
}

func ToSocks5(c net.Conn, host string, port uint16) (net.Conn, error) {
	return &conn{
		init:   make(chan struct{}),
		Conn:   c,
		domain: host,
		port:   port,
	}, nil
}

type conn struct {
	init   chan struct{}
	domain string
	port   uint16
	net.Conn
}

func (c *conn) Read(b []byte) (n int, err error) {
	select {
	case <-c.init:
		return c.Conn.Read(b)
	default:
		return 0, c.clientHandshake()
	}
}

func (c *conn) Write(b []byte) (n int, err error) {
	select {
	case <-c.init:
		return c.Conn.Write(b)
	default:
		return 0, c.clientHandshake()
	}
}

func (c *conn) clientHandshake() error {
	{
		req := &authReq{
			VER:      5,
			NMETHODS: 1,
			METHODS:  [1]byte{0}, // NO AUTHENTICATION REQUIRED
		}
		if err := binary.Write(c.Conn, binary.BigEndian, req); err != nil {
			return xerrors.New(err.Error())
		}
	}
	{
		resp := &authResp{}
		if err := binary.Read(c.Conn, binary.BigEndian, resp); err != nil {
			return xerrors.New(err.Error())
		}
	}
	{
		reqHead := requestHead{
			VER:  5, // socks5
			CMD:  1, // CONNECT
			RSV:  0, // RESERVED
			ATYP: 3, // DOMAINNAME
		}

		if err := binary.Write(c.Conn, binary.BigEndian, reqHead); err != nil {
			return xerrors.New(err.Error())
		}

		buf := make([]byte, 0, 1 /*LEN*/ +len(c.domain)+2 /*PORT*/)
		buf = append(buf, byte(len(c.domain)))
		buf = append(buf, []byte(c.domain)...)
		buf = append(buf, byte(c.port>>8), byte(c.port))
		if _, err := c.Conn.Write(buf); err != nil {
			return xerrors.New(err.Error())
		}
	}
	{
		head := replyHead{}
		if err := binary.Read(c.Conn, binary.BigEndian, &head); err != nil {
			return xerrors.New(err.Error())
		}

		switch head.REP {
		case 0x00:
		default:
			return xerrors.Errorf("socks5 handshake fail, return code: %d", head.REP)
		}

		var bindAddr addrType
		switch head.ATYP {
		case 0x01: // IPv4
			bindAddr = addrTypeIPv4{}
		case 0x03: // domain name
			bindAddr = addrTypeDomain{}
		case 0x04: // IPv6
			bindAddr = addrTypeIPv6{}
		default:
			return xerrors.New("invalid connect type")
		}
		if err := bindAddr.Fullfill(c.Conn); err != nil {
			return xerrors.New(err.Error())
		}
	}

	close(c.init)
	return nil
}
