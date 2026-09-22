package domainops

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func testCertificate(t *testing.T) ([]byte, []byte, *x509.CertPool, time.Time) {
	t.Helper()
	now := time.Now()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(raw)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"api.example.com"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Minute), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err = x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k}), roots, now
}
func TestCertificateValidation(t *testing.T) {
	c, k, roots, now := testCertificate(t)
	if err := ValidateCertificate("api.example.com", c, k, roots, now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, domain string
		cert, key    []byte
		at           time.Time
	}{
		{"wrong domain", "other.example.com", c, k, now}, {"expired", "api.example.com", c, k, now.Add(time.Hour)}, {"future", "api.example.com", c, k, now.Add(-time.Hour)}, {"bad key", "api.example.com", c, []byte("invalid"), now}, {"bad chain", "api.example.com", []byte("bad"), k, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ValidateCertificate(tc.domain, tc.cert, tc.key, roots, tc.at) == nil {
				t.Fatal("accepted invalid certificate")
			}
		})
	}
	if ValidateCertificate("api.example.com", c, k, x509.NewCertPool(), now) == nil {
		t.Fatal("untrusted certificate accepted")
	}
}
