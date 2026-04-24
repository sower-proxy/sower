package socks5

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"

	"errors"
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

const (
	RepSucceeded            = 0x00
	RepGeneralFailure       = 0x01
	RepConnectionNotAllowed = 0x02
)

var (
	noAuthResp = authResp{VER: 5, METHOD: 0}
)

func (s *Socks5) Unwrap(conn net.Conn) (net.Addr, error) {
	addr, err := s.ReadRequest(conn)
	if err != nil {
		return nil, err
	}
	if err := s.WriteReply(conn, RepSucceeded); err != nil {
		return nil, fmt.Errorf("write head: %w", err)
	}
	return addr, nil
}

func (s *Socks5) ReadRequest(conn net.Conn) (net.Addr, error) {
	{ // auth
		auth := new(authReq)
		if err := auth.Fulfill(conn); err != nil || !auth.IsValid() {
			return nil, fmt.Errorf("read auth head: %v, err: %v", auth, err)
		}

		if err := binary.Write(conn, binary.BigEndian, noAuthResp); err != nil {
			return nil, fmt.Errorf("write auth: %w", err)
		}
	}

	var addr addrType
	{ // head
		head := new(reqHead)
		if err := binary.Read(conn, binary.BigEndian, head); err != nil || !head.IsValid() {
			return nil, fmt.Errorf("read head: %v, err: %v", head, err)
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
			return nil, fmt.Errorf("read target: %w", err)
		}
	}

	return &AddrHead{
		addrType: addr,
	}, nil
}

func (s *Socks5) WriteReply(conn net.Conn, rep byte) error {
	head := respHead{VER: 5, REP: rep, RSV: 0, ATYP: 1}
	return binary.Write(conn, binary.BigEndian, head)
}

var noAuthReq = struct {
	VER      byte
	NMETHODS uint8
	METHODS  byte
}{5, 1, 0}
var domainHead = reqHead{VER: 5, CMD: 1, RSV: 0, ATYP: 3}

func (s *Socks5) Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error {
	if len(tgtHost) > 255 {
		return fmt.Errorf("target host too long: %d", len(tgtHost))
	}

	{ // auth
		if err := binary.Write(conn, binary.BigEndian, &noAuthReq); err != nil {
			return err
		}

		resp := &authResp{}
		if err := binary.Read(conn, binary.BigEndian, resp); err != nil {
			return err
		}
		if resp.VER != 5 || resp.METHOD != 0 {
			return fmt.Errorf("unexpected auth response: %+v", resp)
		}
	}
	{ // head
		buf := bytes.NewBuffer(make([]byte, 0, binary.Size(domainHead)+1+len(tgtHost)+2))
		_ = binary.Write(buf, binary.BigEndian, domainHead)
		buf.WriteByte(uint8(len(tgtHost)))
		buf.WriteString(tgtHost)
		buf.Write([]byte{byte(tgtPort >> 8), byte(tgtPort)})

		if n, err := conn.Write(buf.Bytes()); err != nil || n != len(buf.Bytes()) {
			return fmt.Errorf("n: %d, err: %v", n, err)
		}

		head := reqHead{}
		if err := binary.Read(conn, binary.BigEndian, &head); err != nil {
			return err
		}
		if head.VER != 5 || head.RSV != 0 {
			return fmt.Errorf("unexpected response head: %+v", head)
		}
		if head.CMD != 0 {
			return fmt.Errorf("connect rejected, rep=%d", head.CMD)
		}

		var addr addrType
		switch head.ATYP {
		case 0x01:
			addr = &addrTypeIPv4{}
		case 0x03:
			addr = &addrTypeDomain{}
		case 0x04:
			addr = &addrTypeIPv6{}
		default:
			return fmt.Errorf("invalid response ATYP: %d", head.ATYP)
		}
		if err := addr.Fulfill(conn); err != nil {
			return fmt.Errorf("read response address: %w", err)
		}
	}

	return nil
}
