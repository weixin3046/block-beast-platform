package lulu

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCredentialEncryption(t *testing.T) {
	s := NewService(nil, "").WithEncryptionKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	encrypted, e := s.seal("test-secret", "token")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encrypted, []byte("test-secret")) {
		t.Fatal("plaintext stored")
	}
	value, e := s.unseal(encrypted, "token")
	if e != nil || value != "test-secret" {
		t.Fatal("roundtrip", e)
	}
	if _, e = s.unseal(encrypted, "protocol"); e == nil {
		t.Fatal("wrong purpose accepted")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, e = s.unseal(encrypted, "token"); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = NewService(nil, "").seal("secret", "token"); e == nil {
		t.Fatal("missing master key accepted")
	}
}

func TestUpstreamURLValidation(t *testing.T) {
	for _, v := range []string{"ftp://example.invalid", "https://user:secret@example.invalid", "https://example.invalid?token=secret", "https://example.invalid#secret", "not-url"} {
		if validAPIURL(v) {
			t.Errorf("accepted unsafe URL %q", v)
		}
	}
	if !validAPIURL("https://example.invalid/api") {
		t.Fatal("valid HTTPS URL rejected")
	}

}
