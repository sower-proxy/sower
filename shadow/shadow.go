package shadow

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"math/rand"
	"net"

	"github.com/pkg/errors"
)

type conn struct {
	aead         cipher.AEAD
	encryptNonce func() []byte
	decryptNonce func() []byte
	readBuf      []byte
	writeBuf     []byte
	net.Conn
}

func (c *conn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err != nil {
		return n, err
	}

	c.readBuf, err = c.aead.Open(b[:0], c.decryptNonce(), b[:n], nil)
	return len(c.readBuf), err
}

func Shadow(c net.Conn, password string) (net.Conn, error) {
	aead, err := newAEAD(password)
	if err != nil {
		return nil, err
	}

	return &conn{
		aead:         aead,
		encryptNonce: newNonce(password, aead.NonceSize()),
		decryptNonce: newNonce(password, aead.NonceSize()),
		Conn:         c,
	}, nil
}

func (c *conn) Write(b []byte) (n int, err error) {
	c.writeBuf = c.aead.Seal(nil, c.encryptNonce(), b, nil)
	return c.Conn.Write(c.writeBuf)
}

func newAEAD(password string) (cipher.AEAD, error) {
	block, err := aes.NewCipher([]byte(password + password)[:16])
	if err != nil {
		return nil, errors.Wrap(err, "password too short")
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Wrap(err, "GCM")
	}

	return aead, nil
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
