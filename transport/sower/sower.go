package sower

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"net"
	"strconv"

	"github.com/pkg/errors"
)

// https://en.wikipedia.org/wiki/Domain_Name_System
const maxDomainLength = 253

var headSize = binary.Size(new(Head))

// action(>=0x80) + checksum + port + target + data
// data(HTTP, first byte < 0x7F)
type Head struct {
	Cmd      byte
	Checksum uint64
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

func (s *Sower) Unwrap(conn net.Conn) (net.Addr, error) {
	buf := make([]byte, headSize)
	if n, err := conn.Read(buf); err != nil || n != headSize {
		return nil, errors.Errorf("n: %d, err: %s", n, err)
	}

	h := &Head{}
	_ = binary.Read(bytes.NewReader(buf), binary.BigEndian, h)
	switch h.Cmd {
	case 0x80:
	default:
		return nil, errors.Errorf("invalid command: %d", h.Cmd)
	}

	if h.Checksum != sumChecksum(h.TgtAddr, s.password) {
		return nil, errors.New("auth fail")
	}

	return h, nil
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

func sumChecksum(target [maxDomainLength]byte, password []byte) uint64 {
	checksum := md5.Sum(append(target[:], password...))
	checksumVal, _ := binary.Uvarint(checksum[:])
	return checksumVal
}
