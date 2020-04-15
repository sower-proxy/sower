package transport

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strconv"

	"github.com/wweir/sower/util"
)

// checksum(>=0x80) + port + target_length + target + data
// data(HTTP/HTTPS, first byte < 0x7F)
type head struct {
	Checksum byte
	Port     uint16
	AddrLen  uint8
}

func ParseProxyConn(conn net.Conn, password []byte) (net.Conn, string) {
	teeConn := &util.TeeConn{Conn: conn}
	defer teeConn.Stop()

	h := &head{}
	if err := binary.Read(teeConn, binary.BigEndian, h); err != nil || h.Checksum < 0x80 {
		return teeConn, ""
	}

	buf := make([]byte, int(h.AddrLen))
	if _, err := io.ReadFull(teeConn, buf); err != nil {
		return teeConn, ""
	}

	if h.Checksum != sumChecksum(buf, password) {
		return teeConn, ""
	}

	teeConn.Reset()
	return teeConn, net.JoinHostPort(string(buf), strconv.Itoa(int(h.Port)))
}

func ToProxyConn(conn net.Conn, tgtHost string, tgtPort uint16, tlsCfg *tls.Config, password []byte) (net.Conn, error) {
	h := &head{
		Checksum: sumChecksum([]byte(tgtHost), password),
		Port:     tgtPort,
		AddrLen:  uint8(len(tgtHost)),
	}
	if err := binary.Write(conn, binary.BigEndian, h); err != nil {
		conn.Close()
		return nil, err
	}

	var data = []byte(tgtHost)
	var err error
	for n, nn := 0, 0; nn < len(data); nn += n {
		if n, err = conn.Write(data[nn:]); err != nil {
			conn.Close()
			return nil, err
		}
	}

	return conn, nil
}

func sumChecksum(target, password []byte) byte {
	return md5.Sum(append(target, password...))[0] | 0x80
}
