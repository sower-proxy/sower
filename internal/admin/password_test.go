package admin

import (
	"encoding/hex"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	t.Parallel()

	pw := GeneratePassword()
	if len(pw) != 32 {
		t.Fatalf("GeneratePassword() length = %d, want 32 (16 random bytes hex-encoded)", len(pw))
	}
	if _, err := hex.DecodeString(pw); err != nil {
		t.Fatalf("GeneratePassword() is not hex-encoded: %v", err)
	}
	if pw == GeneratePassword() {
		t.Fatal("two GeneratePassword() calls returned the same value")
	}
}
