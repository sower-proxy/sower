package shadow

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
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

	_, err = c.aead.Open(b[:0], c.decryptNonce(), b[:n], nil)
	return n - c.aead.Overhead(), err
}
func (c *conn) Write(b []byte) (n int, err error) {
	c.writeBuf = c.aead.Seal(nil, c.encryptNonce(), b, nil)
	for n < len(c.writeBuf) {
		n, err = c.Conn.Write(c.writeBuf)
		if err != nil && err != io.EOF {
			return 0, err
		}
	}
	return len(b), err
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
