package http

import (
	"bufio"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/wweir/sower/util"
)

const (
	TGT_OTHER byte = iota
	TGT_HTTP
	TGT_HTTPS
)

// Write Addr
type conn struct {
	typ      byte
	password []byte
	domain   []byte
	port     uint16
	init     bool
	net.Conn
}

func NewTgtConn(c net.Conn, password []byte, tgtType byte, domain string, port uint16) net.Conn {
	return &conn{
		typ:      tgtType,
		password: password,
		domain:   []byte(domain),
		port:     port,
		init:     true,
		Conn:     c,
	}
}

// other => type + checksum + port + domain_length ++ domain + data
// http  => type + checksum ++ data
// https => type + checksum + port ++ data
type header struct {
	Type         byte
	Checksum     byte
	Port         uint16
	DomainLength uint8
}

func (c *conn) Write(b []byte) (n int, err error) {
	if c.init {
		c.init = false
		domainLength := byte(len(c.domain))
		if err := binary.Write(c.Conn, binary.BigEndian, &header{
			Type:         c.typ,
			Checksum:     checksum(c.password, c.port, domainLength),
			Port:         c.port,
			DomainLength: domainLength,
		}); err != nil {
			return 0, err
		}

		switch c.typ {
		case TGT_OTHER:
			n, err = c.Conn.Write(append(c.domain, b...))
		default:
			n, err = c.Conn.Write(b)
		}
		return n - len(c.domain), err
	}

	return c.Conn.Write(b)
}

// ParseAddr parse target addr from net.Conn
func ParseAddr(conn net.Conn, password []byte) (_ net.Conn, domain string, port uint16, err error) {
	teeConn := &util.TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	head := new(header)
	if err = binary.Read(conn, binary.BigEndian, head); err != nil {
		return teeConn, "", 0, nil
	}
	if head.Checksum != checksum(password, head.Port, head.DomainLength) {
		return teeConn, "", 0, nil
	}

	switch head.Type {
	case TGT_OTHER:
		buf := make([]byte, int(head.DomainLength))
		if _, err = io.ReadFull(conn, buf); err != nil {
			return teeConn, "", 0, err
		}

		return teeConn, string(buf), head.Port, nil

	case TGT_HTTP:
		teeConn.DropAndRestart()
		return ParseHTTP(teeConn)

	case TGT_HTTPS:
		teeConn.DropAndRestart()
		conn, domain, err = ParseHTTPS(teeConn)
		return conn, domain, head.Port, err

	default:
		return teeConn, "", 0, errors.New("invalid request")
	}
}
func ParseHTTP(teeConn net.Conn) (_ net.Conn, domain string, port uint16, err error) {
	resp, err := http.ReadRequest(bufio.NewReader(teeConn))
	if err != nil {
		return teeConn, "", 0, err
	}

	idx := strings.LastIndex(resp.Host, ":")
	if idx == -1 {
		return teeConn, resp.Host, 80, nil
	}

	p, err := strconv.ParseUint(resp.Host[idx+1:], 10, 16)
	if err != nil {
		return teeConn, "", 0, err
	}
	return teeConn, resp.Host[:idx], uint16(p), nil
}
func ParseHTTPS(teeConn net.Conn) (_ net.Conn, domain string, err error) {
	if domain, _, err = extractSNI(teeConn); err != nil {
		return nil, "", err
	}
	return teeConn, domain, nil
}

var errChecksum = errors.New("invalid checksum")

func checksum(password []byte, port uint16, length uint8) (val byte) {
	nums := md5.Sum(append(password, byte(port), length))
	for _, b := range nums {
		val += b
	}
	return val
}
