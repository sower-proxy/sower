package shadow

import (
	"crypto/cipher"
	"encoding/binary"
	"io"
	"math/rand"
	"net"
)

const MAX_SIZE = 0xFFFF

type conn struct {
	maxSize      int
	aead         cipher.AEAD
	encryptNonce func() []byte
	decryptNonce func() []byte
	writeBuf     []byte
	readBuf      []byte
	readOffset   int
	net.Conn
}

func (c *conn) Read(b []byte) (n int, err error) {
	// read from buffer
	if c.readOffset != 0 {
		dataSize := len(c.readBuf) - c.aead.Overhead()
		n = copy(b, c.readBuf[c.readOffset:dataSize])
		c.readOffset += n

		if c.readOffset == dataSize {
			c.readOffset = 0
		}
		return
	}

	// read from conn
	dataSize := 0
	{ //read data size
		c.readBuf = make([]byte, 2+c.aead.Overhead())
		if _, err = io.ReadFull(c.Conn, c.readBuf); err != nil {
			return
		}
		if _, err = c.aead.Open(c.readBuf[:0], c.decryptNonce(), c.readBuf, nil); err != nil {
			return
		}
		dataSize = int(c.readBuf[0])<<8 + int(c.readBuf[1])
	}
	{ // read data
		c.readBuf = make([]byte, dataSize+c.aead.Overhead())
		if _, err = io.ReadFull(c.Conn, c.readBuf); err != nil {
			return
		}
		if _, err = c.aead.Open(c.readBuf[:0], c.decryptNonce(), c.readBuf, nil); err != nil {
			return
		}
	}

	// buffer extra data
	if n = copy(b, c.readBuf[:dataSize]); n < dataSize {
		c.readOffset = n
	}
	return
}

func (c *conn) Write(b []byte) (n int, err error) {
	bLen := len(b)
	dataSize := MAX_SIZE - (2 + c.aead.Overhead()) - c.aead.Overhead()
	if bLen < c.maxSize {
		dataSize = bLen
	}

	// BigEndian
	c.writeBuf[0], c.writeBuf[1] = byte((dataSize&0xFF00)>>8), byte(dataSize&0xFF)

	c.aead.Seal(c.writeBuf[:0], c.encryptNonce(), c.writeBuf[:2], nil)
	c.aead.Seal(c.writeBuf[:2+c.aead.Overhead()], c.encryptNonce(), b[:dataSize], nil)

	_, err = c.Conn.Write(c.writeBuf[:dataSize+(2+c.aead.Overhead())+c.aead.Overhead()])
	if err != nil {
		return 0, err
	}
	return dataSize, err
}

func Shadow(c net.Conn, cipher, password string) net.Conn {
	aead, err := pickCipher(cipher, password)
	if err != nil {
		panic(err)
	}

	return &conn{
		maxSize:      MAX_SIZE - (2 - aead.Overhead()) - aead.Overhead(),
		aead:         aead,
		encryptNonce: newNonce(password, aead.NonceSize()),
		decryptNonce: newNonce(password, aead.NonceSize()),
		writeBuf:     make([]byte, 0xFFFF),
		Conn:         c,
	}
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
