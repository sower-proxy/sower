package socks5

import (
	"encoding/binary"
	"io"
	"net"
)

// https://tools.ietf.org/html/rfc1928

// 1. client send auth request
type authReq struct {
	VER      byte
	NMETHODS uint8
	METHODS  []byte
}

func (req *authReq) Fulfill(r io.Reader) error {
	buf := make([]byte, 2)
	if n, err := io.ReadFull(r, buf); err != nil || n != 2 {
		return err
	}

	req.VER = buf[0]
	req.NMETHODS = buf[1]

	req.METHODS = make([]byte, int(req.NMETHODS))
	if n, err := io.ReadFull(r, req.METHODS); err != nil || n != len(req.METHODS) {
		return err
	}

	return nil
}

func (r *authReq) IsValid() bool {
	return r.VER == 5 && r.METHODS[0] == 0
}

// 2. server response auth request
type authResp struct {
	VER    byte
	METHOD byte
}

// 3. client request with target address
type reqHead struct {
	VER  byte
	CMD  byte
	RSV  byte
	ATYP byte
}

func (r *reqHead) IsValid() bool {
	return r.VER == 5 && r.CMD == 1
}

// 4. server response with the address that assigned to connect to target address
type respHead struct {
	VER      byte
	REP      byte
	RSV      byte
	ATYP     byte
	BND_ADDR [net.IPv4len]byte
	BND_PORT uint16
}

type addrType interface {
	Fulfill(r io.Reader) error
	Addr() (domain string, port uint16)
}

// ATYP:
//
//	0x01 -> net.IPv4len
//	0x03 -> first byte is length
//	0x04 -> net.IPv6len
type addrTypeIPv4 struct {
	DST_ADDR [4]byte
	DST_PORT uint16
}

func (a *addrTypeIPv4) Fulfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, a)
}

func (a *addrTypeIPv4) Addr() (string, uint16) {
	return net.IP(a.DST_ADDR[:]).String(), a.DST_PORT
}

type addrTypeDomain struct {
	DST_ADDR_LEN uint8
	DST_ADDR     []byte
	DST_PORT     uint16
}

func (a *addrTypeDomain) Fulfill(r io.Reader) error {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}

	a.DST_ADDR_LEN = uint8(buf[0])
	buf = make([]byte, a.DST_ADDR_LEN+2)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}

	a.DST_ADDR = buf[:int(a.DST_ADDR_LEN)]
	a.DST_PORT = binary.BigEndian.Uint16(buf[int(a.DST_ADDR_LEN):])

	return nil
}

func (a *addrTypeDomain) Addr() (string, uint16) {
	return string(a.DST_ADDR[:]), a.DST_PORT
}

type addrTypeIPv6 struct {
	DST_ADDR [16]byte
	DST_PORT uint16
}

func (a *addrTypeIPv6) Fulfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, a)
}

func (a *addrTypeIPv6) Addr() (string, uint16) {
	return net.IP(a.DST_ADDR[:]).String(), a.DST_PORT
}
