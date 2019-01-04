package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"math/rand"

	"github.com/golang/glog"
	"github.com/pkg/errors"
)

type Crypto struct {
	password string
	cipher.AEAD
}

func NewCrypto(password string) (*Crypto, error) {
	aead, err := newAEAD(password)
	if err != nil {
		return nil, errors.Wrap(err, "AEAD")
	}

	return &Crypto{
		password: password,
		AEAD:     aead,
	}, nil
}

func (c *Crypto) Crypto() (encrypt, decrypt func(src []byte) []byte) {
	nonceEncrypt := newNonce(c.password, c.AEAD.NonceSize())
	nonceDecrypt := newNonce(c.password, c.AEAD.NonceSize())

	return func(src []byte) []byte {
			return c.AEAD.Seal(nil, nonceEncrypt(), src, nil)
		},
		func(src []byte) []byte {
			dst, err := c.AEAD.Open(nil, nonceDecrypt(), src, nil)
			if err != nil {
				glog.Fatalf("%+v", errors.Wrap(err, "decrypt"))
			}

			return dst
		}
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
