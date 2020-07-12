package transport

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"

	"golang.org/x/xerrors"
)

// https://tools.ietf.org/html/rfc1928

// 1. client send auth request
type authReq struct {
	VER      byte
	NMETHODS byte
	METHODS  [1]byte // 1 to 255, fix to no authentication
}

// 2. server response auth request
type authResp struct {
	VER    byte
	METHOD byte
}

// 3. client request with target address
type requestHead struct {
	VER  byte
	CMD  byte
	RSV  byte
	ATYP byte
}

// 4. server response with the address that assigned to connect to target address
type replyHead struct {
	VER  byte
	REP  byte
	RSV  byte
	ATYP byte
}

type addrType interface {
	Fullfill(r io.Reader) error
	String() string
}

// ATYP:
//	0x01 -> net.IPv4len
//	0x03 -> first byte is length
//	0x04 -> net.IPv6len
type addrTypeIPv4 struct {
	DST_ADDR [4]byte
	DST_PORT uint16
}

func (a addrTypeIPv4) Fullfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, &a)
}
func (a addrTypeIPv4) String() string {
	return net.JoinHostPort(
		net.IP(a.DST_ADDR[:]).String(),
		strconv.FormatUint(uint64(a.DST_PORT), 10),
	)
}

type addrTypeDomain struct {
	DST_ADDR_LEN uint8
	DST_ADDR     []byte
	DST_PORT     uint16
}

func (a addrTypeDomain) Fullfill(r io.Reader) error {
	buf := make([]byte, 256)
	// domain length
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return xerrors.New(err.Error())
	}
	a.DST_ADDR_LEN = uint8(buf[0])

	// domain
	if _, err := io.ReadFull(r, buf[:int(buf[0])]); err != nil {
		return xerrors.New(err.Error())
	}
	a.DST_ADDR = buf[:int(buf[0])]

	// port
	if _, err := io.ReadFull(r, buf[:2]); err != nil {
		return xerrors.New(err.Error())
	}
	a.DST_PORT = binary.BigEndian.Uint16(buf[:2])

	return nil
}
func (a addrTypeDomain) String() string {
	return net.JoinHostPort(
		net.IP(a.DST_ADDR[:]).String(),
		strconv.FormatUint(uint64(a.DST_PORT), 10),
	)
}

type addrTypeIPv6 struct {
	DST_ADDR [16]byte
	DST_PORT uint16
}

func (a addrTypeIPv6) Fullfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, &a)
}
func (a addrTypeIPv6) String() string {
	return net.JoinHostPort(
		net.IP(a.DST_ADDR[:]).String(),
		strconv.FormatUint(uint64(a.DST_PORT), 10),
	)
}
