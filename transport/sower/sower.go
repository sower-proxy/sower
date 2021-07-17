package sower

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"net"
	"strconv"

	"github.com/wweir/sower/pkg/teeconn"
)

// https://en.wikipedia.org/wiki/Domain_Name_System
const maxDomainLength = 253

var headSize = binary.Size(new(Head))

// action(>=0x80) + checksum + port + target + data
// data(HTTP, first byte < 0x7F)
type Head struct {
	Cmd      byte
	Checksum byte
	Port     uint16
	TgtAddr  [maxDomainLength]byte
}

func (h *Head) Network() string { return "tcp" }
func (h *Head) String() string {
	idx := bytes.IndexRune(h.TgtAddr[:], 0)
	addr := string(h.TgtAddr[:idx])
	return net.JoinHostPort(addr, strconv.Itoa(int(h.Port)))
}

type Sower struct {
	password []byte
}

func New(password string) *Sower {
	return &Sower{
		password: []byte(password),
	}
}

func (s *Sower) Unwrap(conn *teeconn.Conn) net.Addr {
	buf := make([]byte, headSize)
	if n, err := conn.Read(buf); err != nil || n != headSize {
		return nil
	}

	h := &Head{}
	binary.Read(bytes.NewReader(buf), binary.BigEndian, h)
	switch h.Cmd {
	case 0x80:
	default:
		return nil
	}

	if h.Checksum != sumChecksum(h.TgtAddr, s.password) {
		return nil
	}

	return h
}

func (s *Sower) Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error {
	tgtAddr := [maxDomainLength]byte{}
	copy(tgtAddr[:len(tgtHost)], []byte(tgtHost))

	return binary.Write(conn, binary.BigEndian, &Head{
		Cmd:      0x80,
		Checksum: sumChecksum(tgtAddr, s.password),
		Port:     tgtPort,
		TgtAddr:  tgtAddr,
	})
}

func sumChecksum(target [maxDomainLength]byte, password []byte) byte {
	return md5.Sum(append(target[:], password...))[0]
}
