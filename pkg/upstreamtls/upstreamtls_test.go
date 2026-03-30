package upstreamtls

import "testing"

func TestValidateClientHelloAcceptsAliases(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"chrome", "firefox", "randomized_alpn", "randomized-no-alpn", "golang"} {
		if err := ValidateClientHello(value); err != nil {
			t.Fatalf("validate %q: %v", value, err)
		}
	}
}

func TestValidateClientHelloRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	if err := ValidateClientHello("unknown"); err == nil {
		t.Fatal("expected validation error")
	}
}
