package trojan

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strconv"

	"github.com/pkg/errors"
)

// +-----------------------+---------+----------------+---------+----------+
// | hex(SHA224(password)) |  CRLF   | Trojan Request |  CRLF   | Payload  |
// +-----------------------+---------+----------------+---------+----------+
// |          56           | X'0D0A' |    Variable    | X'0D0A' | Variable |
// +-----------------------+---------+----------------+---------+----------+
// +-----+------+----------+----------+
// | CMD | ATYP | DST.ADDR | DST.PORT |
// +-----+------+----------+----------+
// |  1  |  1   | Variable |    2     |
// +-----+------+----------+----------+
// o  CMD
//         o  CONNECT X'01'
//         o  UDP ：X'03'
// o  ATYP
//         o  IP V4 : X'01'
//         o  domain: X'03'
//         o  IP V6 : X'04'

const headLen = 56 + 2 + 1 + 1

type staticHead struct {
	Passwd [56]byte
	CRLF   [2]byte
	CMD    uint8
	ATYP   uint8
}

type ipv4Addr struct {
	ADDR [4]byte
	PORT uint16
	CRLF [2]byte
}

func (*ipv4Addr) Network() string { return "tcp" }
func (a *ipv4Addr) String() string {
	return net.JoinHostPort(net.IP(a.ADDR[:]).String(), strconv.Itoa(int(a.PORT)))
}

type ipv6Addr struct {
	ADDR [16]byte
	PORT uint16
	CRLF [2]byte
}

func (*ipv6Addr) Network() string { return "tcp" }
func (a *ipv6Addr) String() string {
	return net.JoinHostPort(net.IP(a.ADDR[:]).String(), strconv.Itoa(int(a.PORT)))
}

type domain struct {
	ADDR string
	PORT uint16
	CRLF [2]byte
}

func (*domain) Network() string { return "tcp" }
func (a *domain) String() string {
	return net.JoinHostPort(a.ADDR, strconv.Itoa(int(a.PORT)))
}

func (a *domain) Fulfill(r io.Reader) error {
	buf := make([]byte, 1)
	if n, err := io.ReadFull(r, buf); err != nil || n != 1 {
		return errors.New("read domain length failed")
	}

	addrLen := int(buf[0])
	buf = make([]byte, addrLen+4)
	if n, err := io.ReadFull(r, buf); err != nil || n != addrLen+4 {
		return errors.Wrap(err, "read doamin failed")
	}

	a.ADDR = string(buf[:addrLen])
	a.PORT = uint16(buf[addrLen])<<8 + uint16(buf[addrLen+1])
	return nil
}

type Trojan struct {
	headPasswd []byte

	headIPv4   []byte
	headIPv6   []byte
	headDomain []byte
}

func New(password string) *Trojan {
	t := &Trojan{
		headPasswd: make([]byte, 56),
	}
	passSum := sha256.Sum224([]byte(password))
	hex.Encode(t.headPasswd, passSum[:])

	t.headIPv4 = append(t.headPasswd, 0x0D, 0x0A, 0x01, 0x01)
	t.headIPv6 = append(t.headPasswd, 0x0D, 0x0A, 0x01, 0x04)
	t.headDomain = append(t.headPasswd, 0x0D, 0x0A, 0x01, 0x03)
	return t
}

func (t *Trojan) Unwrap(conn net.Conn) (net.Addr, error) {
	buf := make([]byte, headLen)
	// do not use io.ReadFull to avoid hang
	if n, err := conn.Read(buf); err != nil || n != headLen {
		return nil, errors.Errorf("n: %d, err: %s", n, err)
	}

	head := &staticHead{}
	if err := binary.Read(bytes.NewBuffer(buf), binary.BigEndian, head); err != nil {
		return nil, errors.Wrap(err, "read head")
	}

	if !bytes.Equal(head.Passwd[:], []byte(t.headPasswd)) {
		return nil, errors.New("auth fail")
	}

	head.CMD, head.ATYP = buf[58], buf[59]
	switch head.ATYP {
	case 0x01: // ipv4
		addr := &ipv4Addr{}
		err := binary.Read(conn, binary.BigEndian, addr)
		return addr, errors.Wrap(err, "read addr")

	case 0x04: // ipv6
		addr := &ipv6Addr{}
		err := binary.Read(conn, binary.BigEndian, addr)
		return addr, errors.Wrap(err, "read addr")

	case 0x03: // domain
		addr := &domain{}
		err := addr.Fulfill(conn)
		return addr, errors.Wrap(err, "read addr")

	default:
		return nil, errors.New("invalid ATYP")
	}
}

func (t *Trojan) Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error {
	buf := bytes.NewBuffer(make([]byte, 0, headLen+1+len(tgtHost)+4))
	ip := net.ParseIP(tgtHost)
	switch {
	case len(ip.To4()) != 0:
		buf.Write(t.headIPv4)
		buf.Write([]byte(ip.To4()))

	case len(ip) != 0:
		buf.Write(t.headIPv6)
		buf.Write([]byte(ip.To16()))

	default:
		buf.Write(t.headDomain)
		buf.WriteByte(byte(len(tgtHost)))
		buf.WriteString(tgtHost)
	}

	buf.Write([]byte{byte(tgtPort >> 8), byte(tgtPort), 0x0D, 0x0A})

	if n, err := conn.Write(buf.Bytes()); err != nil || n != len(buf.Bytes()) {
		return errors.Errorf("n: %d, err: %s", n, err)
	}
	return nil
}
