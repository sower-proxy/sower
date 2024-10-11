package socks5

import (
	"bytes"
	"encoding/binary"
	"net"
	"strconv"

	"github.com/pkg/errors"
)

type AddrHead struct {
	addrType
}

func (h *AddrHead) Network() string { return "tcp" }
func (h *AddrHead) String() string {
	host, port := h.Addr()
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// Socks5 is a SOCKS5 proxy. It implements the teeconn.Conn interface.
// It is used to be a second relay of other proxy tools.
// user -> sower -socks5-> third-party proxy -> target
type Socks5 struct{}

func New() *Socks5 {
	return &Socks5{}
}

var (
	noAuthResp   = authResp{VER: 5, METHOD: 0}
	succHeadResp = respHead{VER: 5, REP: 0, RSV: 0, ATYP: 1}
)

func (s *Socks5) Unwrap(conn net.Conn) (net.Addr, error) {
	{ // auth
		auth := new(authReq)
		if err := auth.Fulfill(conn); err != nil || !auth.IsValid() {
			return nil, errors.Errorf("read auth head: %v, err: %s", auth, err)
		}

		if err := binary.Write(conn, binary.BigEndian, noAuthResp); err != nil {
			return nil, errors.Wrap(err, "write auth")
		}
	}

	var addr addrType
	{ // head
		head := new(reqHead)
		if err := binary.Read(conn, binary.BigEndian, head); err != nil || !head.IsValid() {
			return nil, errors.Errorf("read head: %v, err: %s", head, err)
		}
		switch head.ATYP {
		case 0x01: // IPv4
			addr = &addrTypeIPv4{}
		case 0x03: // domain name
			addr = &addrTypeDomain{}
		case 0x04: // IPv6
			addr = &addrTypeIPv6{}
		default:
			return nil, errors.New("invalid ATYP")
		}

		if err := addr.Fulfill(conn); err != nil {
			return nil, errors.Wrap(err, "read target")
		}

		if err := binary.Write(conn, binary.BigEndian, succHeadResp); err != nil {
			return nil, errors.Wrap(err, "write head")
		}
	}

	return &AddrHead{
		addrType: addr,
	}, nil
}

var noAuthReq = struct {
	VER      byte
	NMETHODS uint8
	METHODS  byte
}{5, 1, 0}
var domainHead = reqHead{VER: 5, CMD: 1, RSV: 0, ATYP: 3}

func (s *Socks5) Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error {
	{ // auth
		if err := binary.Write(conn, binary.BigEndian, &noAuthReq); err != nil {
			return errors.WithStack(err)
		}

		resp := &authResp{}
		if err := binary.Read(conn, binary.BigEndian, resp); err != nil {
			return errors.WithStack(err)
		}
	}
	{ // head
		buf := bytes.NewBuffer(make([]byte, 0, binary.Size(domainHead)+1+len(tgtHost)+2))
		_ = binary.Write(buf, binary.BigEndian, domainHead)
		buf.WriteByte(uint8(len(tgtHost)))
		buf.WriteString(tgtHost)
		buf.Write([]byte{byte(tgtPort >> 8), byte(tgtPort)})

		if _, err := conn.Write(buf.Bytes()); err != nil {
			return errors.WithStack(err)
		}

		head := respHead{}
		if err := binary.Read(conn, binary.BigEndian, &head); err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}
