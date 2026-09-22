package domainops

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func safePath(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, r := range p {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func RenderNginx(d Domain) ([]byte, error) {
	n, e := NormalizeDomain(d.Name)
	if e != nil {
		return nil, e
	}
	if (d.CertPath == "") != (d.KeyPath == "") {
		return nil, fmt.Errorf("证书必须成对")
	}
	tls := ""
	if d.CertPath != "" {
		if !safePath(d.CertPath) || !safePath(d.KeyPath) {
			return nil, fmt.Errorf("证书路径无效")
		}
		tls = fmt.Sprintf("    listen 443 ssl;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n    ssl_protocols TLSv1.2 TLSv1.3;\n", d.CertPath, d.KeyPath)
	}
	return []byte(fmt.Sprintf(`# Managed by domainctl. Do not edit manually.
server {
    listen 80;
%s    server_name %s;
    client_max_body_size 10m;
    location = /v1/ws {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 60s;
        proxy_send_timeout 60s;
    }
}
`, tls, n)), nil
}

// Positions refer to the original bytes; edits preserve all unrelated text.
type token struct {
	text       string
	start, end int
}
type directive struct {
	words      []string
	children   []directive
	start, end int
}

func parseNginx(src []byte) ([]directive, error) {
	var ts []token
	for i := 0; i < len(src); {
		if unicode.IsSpace(rune(src[i])) {
			i++
			continue
		}
		if src[i] == '#' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		start := i
		if strings.ContainsRune("{};", rune(src[i])) {
			ts = append(ts, token{string(src[i]), i, i + 1})
			i++
			continue
		}
		var b strings.Builder
		quote := byte(0)
		for i < len(src) {
			c := src[i]
			if quote == 0 && (unicode.IsSpace(rune(c)) || strings.ContainsRune("{};#", rune(c))) {
				break
			}
			i++
			if c == '\\' {
				if i == len(src) {
					return nil, fmt.Errorf("Nginx 转义不完整")
				}
				b.WriteByte(src[i])
				i++
				continue
			}
			if c == '\'' || c == '"' {
				if quote == 0 {
					quote = c
					continue
				}
				if quote == c {
					quote = 0
					continue
				}
			}
			b.WriteByte(c)
		}
		if quote != 0 {
			return nil, fmt.Errorf("Nginx 引号不完整")
		}
		ts = append(ts, token{b.String(), start, i})
	}
	at := 0
	var parse func(bool) ([]directive, error)
	parse = func(nested bool) ([]directive, error) {
		var out []directive
		for at < len(ts) {
			if ts[at].text == "}" {
				if !nested {
					return nil, fmt.Errorf("多余 Nginx 右括号")
				}
				at++
				return out, nil
			}
			d := directive{start: ts[at].start}
			for at < len(ts) && ts[at].text != ";" && ts[at].text != "{" && ts[at].text != "}" {
				d.words = append(d.words, ts[at].text)
				at++
			}
			if len(d.words) == 0 || at == len(ts) || ts[at].text == "}" {
				return nil, fmt.Errorf("Nginx 指令不完整")
			}
			delim := ts[at]
			at++
			if delim.text == "{" {
				var err error
				d.children, err = parse(true)
				if err != nil {
					return nil, err
				}
				d.end = ts[at-1].end
			} else {
				d.end = delim.end
			}
			out = append(out, d)
		}
		if nested {
			return nil, fmt.Errorf("Nginx 左括号未闭合")
		}
		return out, nil
	}
	return parse(false)
}
func walkDirectives(ds []directive, fn func(directive) error) error {
	for _, d := range ds {
		if err := fn(d); err != nil {
			return err
		}
		if err := walkDirectives(d.children, fn); err != nil {
			return err
		}
	}
	return nil
}
func ImportDomain(src []byte, name string) ([]byte, Domain, error) {
	name, err := NormalizeDomain(name)
	if err != nil {
		return nil, Domain{}, err
	}
	ds, err := parseNginx(src)
	if err != nil {
		return nil, Domain{}, err
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	domain := Domain{Name: name, Origins: []string{}}
	found := false
	for _, server := range ds {
		if len(server.words) != 1 || server.words[0] != "server" {
			return nil, Domain{}, fmt.Errorf("只支持独立的后端 server 配置")
		}
		var target *directive
		var names []string
		for _, d := range server.children {
			if d.words[0] == "server_name" {
				for _, n := range d.words[1:] {
					if n == name {
						copy := d
						target = &copy
					} else {
						names = append(names, n)
					}
				}
			}
		}
		if target == nil {
			continue
		}
		found = true
		api, ws := false, false
		for _, d := range server.children {
			switch d.words[0] {
			case "listen", "server_name", "ssl_certificate", "ssl_certificate_key", "ssl_protocols", "ssl_session_cache", "ssl_session_timeout", "client_max_body_size", "location":
			default:
				return nil, Domain{}, fmt.Errorf("非标准 server 指令，拒绝丢失既有行为: %s", d.words[0])
			}
			switch d.words[0] {
			case "include", "root", "alias", "return", "rewrite", "if":
				return nil, Domain{}, fmt.Errorf("配置包含不能安全接管的指令")
			case "listen":
				if len(d.words) < 2 || (d.words[1] != "80" && d.words[1] != "443" && d.words[1] != "[::]:80" && d.words[1] != "[::]:443") {
					return nil, Domain{}, fmt.Errorf("只支持标准 HTTP/HTTPS 监听")
				}
			case "client_max_body_size":
				if len(d.words) != 2 || d.words[1] != "10m" {
					return nil, Domain{}, fmt.Errorf("自定义上传限制需人工迁移")
				}
			case "ssl_certificate", "ssl_certificate_key":
				if len(d.words) != 2 || !safePath(d.words[1]) {
					return nil, Domain{}, fmt.Errorf("证书引用无效")
				}
				if d.words[0] == "ssl_certificate" {
					if domain.CertPath != "" && domain.CertPath != d.words[1] {
						return nil, Domain{}, fmt.Errorf("存在多份证书")
					}
					domain.CertPath = d.words[1]
				} else {
					domain.KeyPath = d.words[1]
				}
			case "location":
				path := d.words[len(d.words)-1]
				if path == "/.well-known/acme-challenge/" {
					return nil, Domain{}, fmt.Errorf("ACME 路由需先人工迁移，避免中断续期")
				}
				if path != "/" && path != "/v1/ws" && path != "/.well-known/acme-challenge/" {
					return nil, Domain{}, fmt.Errorf("存在非标准路由，拒绝自动接管")
				}
				for _, child := range d.children {
					switch child.words[0] {
					case "proxy_pass":
					case "proxy_http_version":
						if len(child.words) != 2 || child.words[1] != "1.1" {
							return nil, Domain{}, fmt.Errorf("自定义代理协议需人工迁移")
						}
					case "proxy_read_timeout", "proxy_send_timeout":
						want := "60s"
						if path == "/v1/ws" {
							want = "3600s"
						}
						if len(child.words) != 2 || child.words[1] != want {
							return nil, Domain{}, fmt.Errorf("自定义超时需人工迁移")
						}
					case "root", "default_type":
						if path != "/.well-known/acme-challenge/" {
							return nil, Domain{}, fmt.Errorf("非标准静态路由")
						}
					case "proxy_set_header":
						allowed := map[string]string{"Host": "$host", "X-Real-IP": "$remote_addr", "X-Forwarded-For": "$proxy_add_x_forwarded_for", "X-Forwarded-Proto": "$scheme", "Upgrade": "$http_upgrade", "Connection": "upgrade"}
						if len(child.words) != 3 {
							return nil, Domain{}, fmt.Errorf("代理头格式无效")
						}
						want, ok := allowed[child.words[1]]
						if !ok || (child.words[2] != want && !(child.words[1] == "X-Forwarded-Proto" && (child.words[2] == "http" || child.words[2] == "https"))) {
							return nil, Domain{}, fmt.Errorf("自定义代理头不能自动接管")
						}
					default:
						return nil, Domain{}, fmt.Errorf("非标准路由指令，拒绝丢失既有行为: %s", child.words[0])
					}

					if len(child.children) > 0 || child.words[0] == "include" || child.words[0] == "try_files" || child.words[0] == "alias" {
						return nil, Domain{}, fmt.Errorf("路由无法安全接管")
					}
					if child.words[0] == "proxy_pass" {
						want := "http://127.0.0.1:8080"
						if path == "/v1/ws" {
							want = "http://127.0.0.1:8081"
						}
						if len(child.words) != 2 || child.words[1] != want {
							return nil, Domain{}, fmt.Errorf("仅支持本机后端代理")
						}
						if path == "/" {
							api = true
						}
						if path == "/v1/ws" {
							ws = true
						}
					}
				}
			}
		}
		if !api || !ws {
			return nil, Domain{}, fmt.Errorf("缺少标准 API/WS 代理，拒绝接管")
		}
		if len(names) == 0 {
			edits = append(edits, edit{server.start, server.end, ""})
		} else {
			edits = append(edits, edit{target.start, target.end, "server_name " + strings.Join(names, " ") + ";"})
		}
	}
	if !found {
		return nil, Domain{}, fmt.Errorf("源文件未包含目标域名")
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(append(append([]byte(nil), out[:e.start]...), []byte(e.text)...), out[e.end:]...)
	}
	return out, domain, nil
}
