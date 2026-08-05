package sower

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"errors"
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
	idx := bytes.IndexByte(h.TgtAddr[:], 0)
	if idx < 0 {
		idx = len(h.TgtAddr)
	}
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
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, fmt.Errorf("read head: %w", err)
	}

	h := &Head{}
	if err := binary.Read(bytes.NewReader(buf), binary.BigEndian, h); err != nil {
		return nil, fmt.Errorf("decode head: %w", err)
	}
	switch h.Cmd {
	case 0x80:
	default:
		return nil, fmt.Errorf("invalid command: %d", h.Cmd)
	}

	if h.Checksum != sumChecksum(h.TgtAddr, s.password) {
		return nil, errors.New("auth fail")
	}

	return h, nil
}

func (s *Sower) Wrap(conn net.Conn, tgtHost string, tgtPort uint16) error {
	if len(tgtHost) > maxDomainLength {
		return fmt.Errorf("target host too long: %d", len(tgtHost))
	}

	tgtAddr := [maxDomainLength]byte{}
	copy(tgtAddr[:len(tgtHost)], tgtHost)

	buf := bytes.NewBuffer(make([]byte, 0, headSize))
	if err := binary.Write(buf, binary.BigEndian, &Head{
		Cmd:      0x80,
		Checksum: sumChecksum(tgtAddr, s.password),
		Port:     tgtPort,
		TgtAddr:  tgtAddr,
	}); err != nil {
		return fmt.Errorf("encode head: %w", err)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write head: %w", err)
	}
	return nil
}

func sumChecksum(target [maxDomainLength]byte, password []byte) uint64 {
	h := sha256.New()
	h.Write(target[:])
	h.Write(password)
	var checksum [sha256.Size]byte
	h.Sum(checksum[:0])
	// Take a fixed 64-bit window of the digest. binary.Uvarint would only
	// consume bytes until the first continuation bit is clear (typically the
	// first byte, ~50% of the time), collapsing the checksum to 7 bits of
	// entropy and making the auth check brute-forceable.
	return binary.BigEndian.Uint64(checksum[:8])
}
