package domainops

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"
)

func ValidateCertificate(domain string, certPEM, keyPEM []byte, roots *x509.CertPool, now time.Time) error {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("证书或私钥无效，或两者不匹配")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("证书解析失败")
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(raw)
		if e != nil {
			return fmt.Errorf("中间证书解析失败")
		}
		intermediates.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: domain, Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	if err != nil {
		return fmt.Errorf("证书域名、有效期或信任链检查失败: %w", err)
	}
	return nil
}
