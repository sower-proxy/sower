package shadow

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
	"log"
	"math/rand"
	"net"

	"github.com/pkg/errors"
)

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
}

var MTU = 1350 //MTU must little than 0xFFFF

type conn struct {
	dataSize     int
	aead         cipher.AEAD
	encryptNonce func() []byte
	decryptNonce func() []byte
	writeBuf     []byte
	readBuf      []byte
	readOffset   int
	net.Conn
}

func (c *conn) Read(b []byte) (n int, err error) {
	bLength := len(b)
	offset := 0
	if c.readOffset != 0 {
		offset = copy(b, c.readBuf[c.readOffset:])
		if offset+c.readOffset < c.dataSize {
			c.readOffset += offset
			return offset, nil
		}
		c.readOffset = 0
	}

	for ; offset < bLength; offset += c.readOffset {
		n, err = io.ReadFull(c.Conn, c.readBuf)
		if err != nil {
			return offset, err
		}

		_, err = c.aead.Open(c.readBuf[:0], c.decryptNonce(), c.readBuf, nil)
		if err != nil {
			return offset, err
		}

		// BigEndian
		payloadSize := int(c.readBuf[0])<<8 + int(c.readBuf[1])
		c.readOffset = copy(b[offset:], c.readBuf[2:2+payloadSize])
		if c.readOffset < payloadSize { // b is full
			c.readOffset += 2
			return len(b), nil
		}
	}

	c.readOffset = 0
	log.Println(offset)
	return len(b), nil
}

func (c *conn) Write(b []byte) (n int, err error) {
	bLength := len(b)
	size := 0

	for offset := 0; offset < bLength; offset += size {
		if offset+c.dataSize <= bLength {
			size = c.dataSize
		} else {
			size = bLength - offset
		}

		// BigEndian
		c.writeBuf[0], c.writeBuf[1] = byte((size&0xFF00)>>8), byte(size&0xFF)
		copy(c.writeBuf[2:size+2], b[offset:offset+size])

		c.aead.Seal(c.writeBuf[:0], c.encryptNonce(), c.writeBuf[:c.dataSize+2], nil)
		_, err = c.Conn.Write(c.writeBuf)
		if err != nil && err != io.EOF {
			return offset, err
		}
	}
	return len(b), err
}

func Shadow(c net.Conn, password string) (net.Conn, error) {
	block, err := aes.NewCipher([]byte(password + password)[:16])
	if err != nil {
		return nil, errors.Wrap(err, "password too short")
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Wrap(err, "GCM")
	}

	return &conn{
		dataSize:     MTU - aead.Overhead() - 2,
		aead:         aead,
		encryptNonce: newNonce(password, aead.NonceSize()),
		decryptNonce: newNonce(password, aead.NonceSize()),
		writeBuf:     make([]byte, MTU),
		readBuf:      make([]byte, MTU),
		Conn:         c,
	}, nil
}

func newNonce(password string, size int) func() []byte {
	num, _ := binary.Varint([]byte(password))
	rnd := rand.New(rand.NewSource(num))

	buf := make([]byte, size)
	return func() []byte {
		rnd.Read(buf)
		return buf
	}
}
