package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDomainsInvalidInputsNeverConnect(t *testing.T) {
	for _, args := range [][]string{{"invalid", "list"}, {"production", "add", "bad;domain"}, {"production", "add", "api.example.com", "--cert", "missing.pem"}} {
		dir := t.TempDir()
		marker := filepath.Join(dir, "network")
		writeFixture(t, filepath.Join(dir, "ssh"), "#!/bin/sh\ntouch \"$DOMAIN_NETWORK_MARKER\"\nexit 1\n", 0755)
		writeFixture(t, filepath.Join(dir, "scp"), "#!/bin/sh\ntouch \"$DOMAIN_NETWORK_MARKER\"\nexit 1\n", 0755)
		c := exec.Command("bash", append([]string{"domains.sh"}, args...)...)
		c.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "DOMAIN_NETWORK_MARKER="+marker)
		if b, err := c.CombinedOutput(); err == nil {
			t.Fatalf("accepted invalid input: %s", b)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatal("invalid request reached network")
		}
	}
}
func TestDomainDryRunUsesReadOnlyRemoteCheck(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	writeFixture(t, filepath.Join(dir, "ssh"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$DOMAIN_CAPTURE\"\ncat > \"$DOMAIN_CAPTURE.request\"\n", 0755)
	writeFixture(t, filepath.Join(dir, "scp"), "#!/bin/sh\nexit 99\n", 0755)
	c := exec.Command("bash", "domains.sh", "staging", "add", "api.example.com", "--dry-run")
	c.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "DOMAIN_CAPTURE="+capture)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	b, _ := os.ReadFile(capture)
	if !strings.Contains(string(b), "domainctl check") || strings.Contains(string(b), "mkdir") {
		t.Fatalf("not read only: %s", b)
	}
}
