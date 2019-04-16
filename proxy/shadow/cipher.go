package shadow

import (
	"crypto/aes"
	"crypto/cipher"

	"github.com/pkg/errors"
	"golang.org/x/crypto/chacha20poly1305"
)

//go:generate stringer -type=typ $GOFILE
type typ int

const (
	AES_128_GCM typ = iota
	AES_192_GCM
	AES_256_GCM
	CHACHA20_IETF_POLY1305
	XCHACHA20_IETF_POLY1305
	cipherEnd
)

func ListCiphers() []string {
	list := make([]string, 0, int(cipherEnd))
	for i := typ(0); i < cipherEnd; i++ {
		list = append(list, i.String())
	}
	return list
}

func pickCipher(typ, password string) (cipher.AEAD, error) {
	var blockSize int
	switch typ {
	case AES_128_GCM.String():
		blockSize = 16
	case AES_192_GCM.String():
		blockSize = 24
	case AES_256_GCM.String():
		blockSize = 32

	case CHACHA20_IETF_POLY1305.String():
		return chacha20poly1305.New(genKey(password, 256))
	case XCHACHA20_IETF_POLY1305.String():
		return chacha20poly1305.NewX(genKey(password, 256))

	default:
		return nil, errors.New("do not support cipher type: " + typ)
	}

	// aes gcm
	block, err := aes.NewCipher(genKey(password, blockSize))
	if err != nil {
		return nil, errors.Wrap(err, "password")
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.Wrap(err, AES_128_GCM.String())
	}
	return aead, nil
}

func genKey(filler string, size int) []byte {
	res := make([]byte, size)
	if filler == "" {
		panic("password should not be empty")
	}

	fillerByte := []byte(filler)
	length := len(fillerByte)
	for i := 0; ; i++ {
		if copy(res[i*length:], fillerByte) != length {
			return res
		}
	}
}
