package parser

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/wweir/sower/util"
)

const (
	ANY byte = iota
	HTTP
	HTTPS
)

// Write Addr
type conn struct {
	typ    byte
	domain string
	port   string
	init   bool
	net.Conn
}

func NewAnyProtocol(c net.Conn, domain, port string) net.Conn {
	return &conn{
		typ:    ANY,
		domain: domain,
		port:   port,
		Conn:   c,
	}
}
func NewHttpProtocol(c net.Conn) net.Conn {
	return &conn{
		typ:  HTTP,
		Conn: c,
	}
}
func NewHttpsProtocol(c net.Conn, port string) net.Conn {
	return &conn{
		typ:  HTTPS,
		port: port,
		Conn: c,
	}
}

func (c *conn) Write(b []byte) (n int, err error) {
	if !c.init {
		var pkg []byte
		switch c.typ {
		case ANY:
			pkg = make([]byte, 0, 1+len(c.domain)+1+len(c.port)+len(b))
			pkg = append(pkg, ANY)
			pkg = append(pkg, byte(len(c.domain)+1+len(c.port)))
			pkg = append(pkg, []byte(c.domain+":"+c.port)...)

		case HTTP:
			pkg = make([]byte, 0, 1+len(b))
			pkg = append(pkg, HTTP)

		case HTTPS:
			pkg = make([]byte, 0, 1+2+len(b))
			pkg = append(pkg, HTTPS)
			port, _ := strconv.Atoi(c.port)
			pkg = append(pkg, byte(port>>8), byte(port))
		}

		c.init = true
		return c.Conn.Write(append(pkg, b...))
	}

	return c.Conn.Write(b)
}

// Read Addr
func ParseAddr(conn net.Conn) (net.Conn, string, string, error) {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return conn, "", "", err
	}

	switch buf[0] {
	case ANY:
		if _, err := io.ReadFull(conn, buf); err != nil {
			return conn, "", "", err
		}
		buf = make([]byte, int(buf[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return conn, "", "", err
		}

		addr := string(buf)
		if idx := strings.LastIndex(addr, ":"); idx != -1 {
			return conn, addr[:idx], addr[idx+1:], nil
		}
		return conn, "", "", errors.New("invalid payload")

	case HTTP:
		return ParseHttpAddr(conn)

	case HTTPS:
		buf = make([]byte, 2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return conn, "", "", err
		}

		conn, domain, err := ParseHttpsHost(conn)
		return conn, domain, strconv.Itoa(int(buf[0])<<8 + int(buf[1])), err

	default:
		return conn, "", "", errors.Errorf("not supported type (%v)", buf[0])
	}
}

func ParseHttpAddr(conn net.Conn) (net.Conn, string, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	b := bufio.NewReader(teeConn)
	resp, err := http.ReadRequest(b)
	if err != nil {
		return teeConn, "", "", err
	}

	if idx := strings.LastIndex(resp.Host, ":"); idx != -1 {
		return teeConn, resp.Host[:idx], resp.Host[idx+1:], nil
	}
	return teeConn, resp.Host, "80", nil
}

func ParseHttpsHost(conn net.Conn) (net.Conn, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	domain, _, err := extractSNI(io.Reader(teeConn))
	if err != nil {
		return teeConn, "", err
	}

	return teeConn, domain, nil
}
