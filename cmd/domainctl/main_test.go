package main

import (
	"encoding/json"
	"github.com/block-beast/platform/internal/platform/domainops"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareRejectsInjectionAndUnpairedCertificate(t *testing.T) {
	for _, args := range [][]string{{"add", "x.example;bad"}, {"add", "api.example.com", "--cert", "file"}, {"add", "api.example.com", "--allow-origin", "*"}, {"remove", "api.example.com", "--key", "secret"}, {"add", "api.example.com", "--unknown"}} {
		if _, err := prepare(t.TempDir(), "/tmp/block-beast-domain-0123456789abcdef", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestPrepareRequestAndNoPrivateKeyForDryRun(t *testing.T) {
	dir := t.TempDir()
	r, err := prepare(dir, "/tmp/block-beast-domain-0123456789abcdef", []string{"add", "API.example.com", "--allow-origin", "https://web.example.com", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.DryRun || r.Domain != "api.example.com" {
		t.Fatal(r)
	}
	b, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got domainops.Request
	if json.Unmarshal(b, &got) != nil {
		t.Fatal("invalid request")
	}
	if strings.Contains(string(b), "PRIVATE KEY") {
		t.Fatal("key in request")
	}
	if _, err = os.Stat(filepath.Join(dir, "privkey.pem")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote private key")
	}
}
