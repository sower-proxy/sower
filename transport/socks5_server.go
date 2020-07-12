package transport

import (
	"encoding/binary"
	"net"

	"golang.org/x/xerrors"
)

func ParseSocks5(conn net.Conn) (tgtaddr string, err error) {
	{
		authReq := new(authReq)
		if err := binary.Read(conn, binary.BigEndian, authReq); err != nil {
			return "", xerrors.New(err.Error())
		}

		if authReq.VER != 5 || // socks5
			authReq.NMETHODS != 1 ||
			authReq.METHODS[0] != 0 { // NO_AUTH
			return "", xerrors.New("invalid socks5 auth method")
		}
	}
	{
		if err := binary.Write(conn, binary.BigEndian, &authResp{
			VER:    5,
			METHOD: 1,
		}); err != nil {
			return "", xerrors.New(err.Error())
		}
	}
	{
		head := &requestHead{}
		if err := binary.Read(conn, binary.BigEndian, head); err != nil {
			return "", xerrors.New(err.Error())
		}
		if head.VER != 5 ||
			head.CMD != 1 {
			return "", xerrors.New("invalid socks5 connect request")
		}

		var addr addrType
		switch head.ATYP {
		case 0x01: // IPv4
			addr = addrTypeIPv4{}
		case 0x03: // domain name
			addr = addrTypeDomain{}
		case 0x04: // IPv6
			addr = addrTypeIPv6{}
		default:
			return "", xerrors.New("invalid connect type")
		}
		if err := addr.Fullfill(conn); err != nil {
			return "", xerrors.New(err.Error())
		}
		tgtaddr = addr.String()
	}
	{
		if err := binary.Write(conn, binary.BigEndian, &replyHead{
			VER:  5,
			REP:  0,
			RSV:  0,
			ATYP: 1,
		}); err != nil {
			return "", xerrors.New(err.Error())
		}

		// FIXME: return the real address
		if _, err := conn.Write(make([]byte, 6)); err != nil {
			return "", xerrors.New(err.Error())
		}
	}

	return tgtaddr, nil
}
