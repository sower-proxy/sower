package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"strconv"

	"github.com/wweir/sower/util"
	"golang.org/x/xerrors"
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

type Trojan struct {
	Password [56]byte
	TrojanRequest
}
type TrojanRequest struct {
	CMD     uint8
	ATYP    uint8
	DstAddr []byte
	DstPort uint16
}

func (t *TrojanRequest) Addr() string {
	switch t.ATYP {
	case 0x01:
		fallthrough
	case 0x04:
		return net.JoinHostPort(net.IP(t.DstAddr).String(), strconv.Itoa(int(t.DstPort)))
	case 0x03:
		return string(t.DstAddr) + ":" + strconv.Itoa(int(t.DstPort))
	default:
		panic("invalid ATYP")
	}
}

func ParseTrojanConn(conn net.Conn, password []byte) (net.Conn, *Trojan) {
	passData := sha256.Sum224(password)
	passHead := hex.EncodeToString(passData[:])

	teeConn := &util.TeeConn{Conn: conn}
	defer teeConn.Stop()

	buf := make([]byte, 56+2+1+1+1)
	if _, err := io.ReadFull(teeConn, buf); err != nil {
		return teeConn, nil
	}
	if string(buf[:56]) != passHead {
		return teeConn, nil
	}

	t := Trojan{}
	t.CMD, t.ATYP = buf[58], buf[59]
	addrLen := buf[60]
	switch t.ATYP {
	case 0x01: //ipv4
		buf = make([]byte, net.IPv4len+2+2)
		buf[0] = addrLen
		if _, err := io.ReadFull(teeConn, buf[1:]); err != nil {
			return teeConn, nil
		}

	case 0x04: //ipv6
		buf = make([]byte, net.IPv6len+2+2)
		buf[0] = addrLen
		if _, err := io.ReadFull(teeConn, buf[1:]); err != nil {
			return teeConn, nil
		}

	case 0x03: // domain
		buf = make([]byte, addrLen+2+2)
		buf[0] = addrLen
		if _, err := io.ReadFull(teeConn, buf); err != nil {
			return teeConn, nil
		}
	default:
		return teeConn, nil
	}

	t.DstAddr = buf[:len(buf)-4]
	t.DstPort = uint16(buf[len(buf)-4])<<8 + uint16(buf[len(buf)-3])

	teeConn.Reset()
	return teeConn, &t
}

func ToTrojanConn(conn net.Conn, tgtHost string, tgtPort uint16, password []byte) (net.Conn, error) {
	passData := sha256.Sum224(password)
	passHead := hex.EncodeToString(passData[:])

	buf := make([]byte, 0, 56+2+1+1+1+len(tgtHost)+2+2)
	buf = append(buf, []byte(passHead)...)
	buf = append(buf, '\r', '\n')
	buf = append(buf, 1, 3, uint8(len(tgtHost)))
	buf = append(buf, []byte(tgtHost)...)
	buf = append(buf, byte(tgtPort>>8), byte(tgtPort))
	buf = append(buf, '\r', '\n')

	if n, err := conn.Write(buf); err != nil || n != len(buf) {
		return conn, xerrors.Errorf("n: %d, msg: %s", n, err)
	}
	return conn, nil
}
