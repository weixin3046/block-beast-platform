package domainops

import (
	"strings"
	"testing"
)

func TestNginxHTTPAndTLS(t *testing.T) {
	d := Domain{Name: "api.example.com"}
	b, e := RenderNginx(d)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	if strings.Contains(s, "443") || strings.Contains(s, "301") {
		t.Fatal(s)
	}
	for _, v := range []string{"listen 80;", "location = /v1/ws", "127.0.0.1:8081", "127.0.0.1:8080", "proxy_set_header X-Forwarded-Proto $scheme;"} {
		if !strings.Contains(s, v) {
			t.Fatal(v)
		}
	}
	d.CertPath = "/etc/block-beast/domains/certs/id/fullchain.pem"
	d.KeyPath = "/etc/block-beast/domains/certs/id/privkey.pem"
	b, e = RenderNginx(d)
	if e != nil || !strings.Contains(string(b), "443 ssl;") || !strings.Contains(string(b), "listen 80;") {
		t.Fatalf("%s %v", b, e)
	}
	d.KeyPath = "/tmp/key;bad"
	if _, e = RenderNginx(d); e == nil {
		t.Fatal("unsafe path accepted")
	}
}

const legacyNginx = `# keep comment
server {
 listen 80;
 server_name api.old.example.com api.other.example.com;
 location /v1/ws { proxy_pass http://127.0.0.1:8081; }
 location / { proxy_pass http://127.0.0.1:8080; }
}
server {
 listen 443 ssl;
 server_name api.old.example.com api.other.example.com;
 ssl_certificate /etc/cert.pem;
 ssl_certificate_key /etc/key.pem;
 location / { proxy_pass http://127.0.0.1:8080; }
 location /v1/ws { proxy_pass http://127.0.0.1:8081; }
}
`

func TestImportPreservesOtherNamesAndRejectsFrontend(t *testing.T) {
	b, d, e := ImportDomain([]byte(legacyNginx), "api.old.example.com")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "api.old.example.com") || strings.Count(string(b), "api.other.example.com") != 2 || d.CertPath != "/etc/cert.pem" {
		t.Fatalf("%s %+v", b, d)
	}
	for _, s := range []string{
		`server {server_name api.old.example.com;root /www/h5;location / {try_files $uri /index.html;}}`,
		`server {server_name api.old.example.com;include unknown.conf;location / {proxy_pass http://127.0.0.1:8080;}}`,
		strings.ReplaceAll(legacyNginx, "127.0.0.1:8080", "external.example.com"),
	} {
		if _, _, e := ImportDomain([]byte(s), "api.old.example.com"); e == nil {
			t.Fatal("unsafe import accepted")
		}
	}
}
func TestImportRejectsSecurityRulesInsteadOfDroppingThem(t *testing.T) {
	for _, rule := range []string{"auth_basic secret;", "allow 127.0.0.1; deny all;", "limit_req zone=login;", "proxy_set_header Authorization secret;"} {
		src := strings.Replace(legacyNginx, "location / { proxy_pass", "location / { "+rule+" proxy_pass", 1)
		if _, _, err := ImportDomain([]byte(src), "api.old.example.com"); err == nil {
			t.Fatalf("would drop security rule %s", rule)
		}
	}
}
