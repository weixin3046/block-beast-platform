package lulu

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestProtocolConfiguration(t *testing.T) {
	for _, v := range []string{"http://example.invalid:5022/", "https://example.invalid"} {
		if !validAPIURL(v) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"example.invalid:5022", "ftp://example.invalid", "http://user:pass@example.invalid", "http://example.invalid?q=1", "http://example.invalid/#x"} {
		if validAPIURL(v) {
			t.Fatal(v)
		}
	}
	for _, n := range []int{0, 16, 23, 24, 25, 31, 32, 33} {
		v := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, n))
		if ValidProtocolKey(v) != (n == 24 || n == 32) {
			t.Fatal(n)
		}
	}
	if ValidProtocolKey("invalid!") {
		t.Fatal("invalid base64 accepted")
	}
}
