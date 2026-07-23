package pqpa

import (
	"testing"
)

func TestSignatureIsDeterministic(t *testing.T) {
	client := NewClient("https://example.test", "key", "secret", nil)
	got := client.Signature("POST", "/v1/test", "1700000000000", "nonce", []byte(`{"amount":1}`))
	if got == "" || got != client.Signature("POST", "/v1/test", "1700000000000", "nonce", []byte(`{"amount":1}`)) {
		t.Fatalf("signature should be deterministic and non-empty: %q", got)
	}
}
