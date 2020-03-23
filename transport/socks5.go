package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
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
	<-c.init
	return c.Conn.Read(b)
}

func (c *conn) Write(b []byte) (n int, err error) {
	select {
	case <-c.init:
		return c.Conn.Write(b)
	default:
	}

	{
		req := &authReq{
			VER:      5,
			NMETHODS: 1,
			METHODS:  [1]byte{0}, // NO AUTHENTICATION REQUIRED
		}
		if err := binary.Write(c.Conn, binary.BigEndian, req); err != nil {
			return 0, err
		}
	}
	{
		resp := &authResp{}
		if err := binary.Read(c.Conn, binary.BigEndian, resp); err != nil {
			return 0, err
		}
	}
	{
		portBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(portBuf, c.port)
		req := &request{
			req: req{
				VER:  5, // socks5
				CMD:  1, // CONNECT
				RSV:  0, // RESERVED
				ATYP: 3, // DOMAINNAME
			},
			DST_ADDR: append([]byte{byte(len(c.domain))}, []byte(c.domain)...),
			DST_PORT: portBuf,
		}

		if _, err := c.Conn.Write(req.Bytes()); err != nil {
			return 0, err
		}
	}
	{
		resp := &response{}
		if err := binary.Read(c.Conn, binary.BigEndian, &(resp.resp)); err != nil {
			return 0, err
		}

		switch resp.REP {
		case 0x00:
		default:
			return 0, fmt.Errorf("socks5 handshake fail, return code: %d", resp.REP)
		}

		switch resp.ATYP {
		case 0x01: // IPv4
			resp.DST_ADDR = make([]byte, net.IPv4len)
			if _, err := io.ReadFull(c.Conn, resp.DST_ADDR); err != nil {
				return 0, err
			}
		case 0x03: // domain name
			if _, err := io.ReadFull(c.Conn, resp.DST_ADDR[:1]); err != nil {
				return 0, err
			}
			if _, err := io.ReadFull(c.Conn, resp.DST_ADDR[1:1+int(resp.DST_ADDR[0])]); err != nil {
				return 0, err
			}
		case 0x04:
			resp.DST_ADDR = make([]byte, net.IPv6len)
			if _, err := io.ReadFull(c.Conn, resp.DST_ADDR); err != nil {
				return 0, err
			}
		}

		resp.DST_PORT = make([]byte, 2)
		if _, err := io.ReadFull(c.Conn, resp.DST_PORT); err != nil {
			return 0, err
		}
	}

	close(c.init)
	return c.Conn.Write(b)
}
