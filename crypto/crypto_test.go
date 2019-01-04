package crypto

import (
	"reflect"
	"testing"
)

func TestCrypto_Crypto(t *testing.T) {
	mockCrypto, err := NewCrypto("12345678")
	if err != nil {
		t.Errorf("%s", err)
	}

	tests := []struct {
		name string
		data []byte
	}{{
		"",
		[]byte("123"),
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEncrypt, gotDecrypt := mockCrypto.Crypto()

			if !reflect.DeepEqual(tt.data, gotDecrypt(gotEncrypt(tt.data))) {
				t.Errorf("Crypto.Crypto() raw = %v, got = %v", tt.data, gotDecrypt(gotEncrypt(tt.data)))
			}
		})
	}
}
