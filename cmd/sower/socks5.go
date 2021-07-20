package main

import (
	"encoding/binary"
	"io"
	"net"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/router"
)

func ServeSocks5(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).
			Msg("serve socks5")
	}
	go ServeSocks5(ln, r)
	defer conn.Close()
	start := time.Now()

	{
		auth := new(socks5AuthReq)
		if err := auth.Fulfill(conn); err != nil {
			log.Error().Err(err).
				Interface("request", auth).
				Msg("socks5 auth")
			return
		}

		if err := binary.Write(conn, binary.BigEndian, socks5AuthResp); err != nil {
			log.Error().Err(err).
				Msg("socks5 auth")
			return
		}
	}

	var addr addrType
	{
		head := new(socks5HeadReq)
		if err := binary.Read(conn, binary.BigEndian, head); err != nil || !head.IsValid() {
			return
		}
		switch head.ATYP {
		case 0x01: // IPv4
			addr = &addrTypeIPv4{}
		case 0x03: // domain name
			addr = &addrTypeDomain{}
		case 0x04: // IPv6
			addr = &addrTypeIPv6{}
		default:
			log.Error().Err(err).
				Interface("head", head).
				Msg("socks5 connect")
			return
		}

		if err := addr.Fulfill(conn); err != nil {
			return
		}

		if err := binary.Write(conn, binary.BigEndian, socks5HeadResp); err != nil {
			log.Error().Err(err).
				Msg("socks5 head")
			return
		}
	}

	host, port := addr.Addr()
	route, err := r.RouteHandle(conn, host, port)
	switch route {
	case router.RouteProxy:
	case router.RouteDefault:
	default:
		return
	}
	log.Err(err).
		Str("host", host).
		Uint16("port", port).
		Str("route", route).
		Dur("spend", time.Since(start)).
		Msg("serve socsk5")
}

/******************* https://tools.ietf.org/html/rfc1928 *******************/

// 1. client send auth request
type socks5AuthReq struct {
	VER      byte
	NMETHODS uint8
	METHODS  []byte
}

func (req *socks5AuthReq) Fulfill(r io.Reader) error {
	buf := make([]byte, 2)
	if n, err := r.Read(buf); err != nil || n != 2 {
		return err
	}

	req.VER = buf[0]
	req.NMETHODS = buf[1]

	req.METHODS = make([]byte, int(req.NMETHODS))
	if n, err := r.Read(req.METHODS); err != nil || n != len(req.METHODS) {
		return err
	}

	return nil
}

func (r *socks5AuthReq) IsValid() bool {
	return r.VER == 5 && r.METHODS[0] == 0
}

// 2. server response auth request
var socks5AuthResp = struct {
	VER    byte
	METHOD byte
}{VER: 5, METHOD: 0}

// 3. client request with target address
type socks5HeadReq struct {
	VER  byte
	CMD  byte
	RSV  byte
	ATYP byte
}

func (r *socks5HeadReq) IsValid() bool {
	return r.VER == 5 && r.CMD == 1
}

// 4. server response with the address that assigned to connect to target address
var socks5HeadResp = struct {
	VER  byte
	REP  byte
	RSV  byte
	ATYP byte
	BIND struct {
		ADDR [4]byte
		PORT uint16
	}
}{VER: 5, REP: 0, RSV: 0, ATYP: 1}

type addrType interface {
	Fulfill(r io.Reader) error
	Addr() (domain string, port uint16)
}

// ATYP:
//	0x01 -> net.IPv4len
//	0x03 -> first byte is length
//	0x04 -> net.IPv6len
type addrTypeIPv4 struct {
	DST_ADDR [4]byte
	DST_PORT uint16
}

func (a *addrTypeIPv4) Fulfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, &a)
}
func (a *addrTypeIPv4) Addr() (string, uint16) {
	return net.IP(a.DST_ADDR[:]).String(), a.DST_PORT
}

type addrTypeIPv6 struct {
	DST_ADDR [16]byte
	DST_PORT uint16
}

func (a *addrTypeIPv6) Fulfill(r io.Reader) error {
	return binary.Read(r, binary.BigEndian, &a)
}
func (a *addrTypeIPv6) Addr() (string, uint16) {
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
