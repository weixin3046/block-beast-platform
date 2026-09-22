// Package domainops manages backend ingress configuration, not business data.
package domainops

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Request struct {
	LocalTLSChecked bool     `json:"local_tls_checked,omitempty"`
	SessionPath     string   `json:"session_path,omitempty"`
	Operation       string   `json:"operation"`
	Domain          string   `json:"domain"`
	OldDomain       string   `json:"old_domain,omitempty"`
	Origins         []string `json:"origins,omitempty"`
	CertPath        string   `json:"cert_path,omitempty"`
	KeyPath         string   `json:"key_path,omitempty"`
	Source          string   `json:"source,omitempty"`
	Address         string   `json:"address,omitempty"`
	DryRun          bool     `json:"dry_run"`
}
type Domain struct {
	Name     string   `json:"name"`
	Origins  []string `json:"origins"`
	CertPath string   `json:"cert_path,omitempty"`
	KeyPath  string   `json:"key_path,omitempty"`
}
type State struct {
	Version int               `json:"version"`
	Domains map[string]Domain `json:"domains"`
}

var labelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func NormalizeDomain(input string) (string, error) {
	s := strings.ToLower(input)
	if len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil {
		return "", fmt.Errorf("必须提供明确的 DNS 域名")
	}
	for _, label := range strings.Split(s, ".") {
		if !labelRE.MatchString(label) {
			return "", fmt.Errorf("域名格式无效")
		}
	}
	return s, nil
}
func NormalizeOrigin(input string) (string, error) {
	u, err := url.Parse(input)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(input, "#") {
		return "", fmt.Errorf("Origin 必须是无路径的 http(s)://主机[:端口]")
	}
	host, err := NormalizeDomain(u.Hostname())
	if err != nil {
		return "", err
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("Origin 端口无效")
		}
		host = net.JoinHostPort(host, port)
	} else if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("Origin 端口为空")
	}
	return u.Scheme + "://" + host, nil
}
func ManagedOrigins(s State) []string {
	seen := map[string]bool{}
	for _, d := range s.Domains {
		for _, o := range d.Origins {
			seen[o] = true
		}
	}
	out := make([]string, 0, len(seen))
	for o := range seen {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}
func NextState(s State, r Request) (State, error) {
	if s.Version != 1 {
		return State{}, fmt.Errorf("不支持的域名状态版本")
	}
	name, err := NormalizeDomain(r.Domain)
	if err != nil {
		return State{}, err
	}
	if (r.CertPath == "") != (r.KeyPath == "") {
		return State{}, fmt.Errorf("证书和私钥必须成对提供")
	}
	out := State{Version: 1, Domains: map[string]Domain{}}
	for n, d := range s.Domains {
		d.Origins = append([]string(nil), d.Origins...)
		out.Domains[n] = d
	}
	d, exists := out.Domains[name]
	switch r.Operation {
	case "remove":
		if !exists {
			return State{}, fmt.Errorf("域名未受本工具管理")
		}
		delete(out.Domains, name)
		return out, nil
	case "replace":
		old, e := NormalizeDomain(r.OldDomain)
		if e != nil {
			return State{}, e
		}
		if old == name {
			return State{}, fmt.Errorf("新旧域名不能相同")
		}
		previous, ok := out.Domains[old]
		if !ok {
			return State{}, fmt.Errorf("旧域名未受本工具管理")
		}
		if exists {
			return State{}, fmt.Errorf("新域名已存在，请单独移除旧域名")
		}
		d = Domain{Name: name, Origins: append([]string(nil), previous.Origins...)}
		delete(out.Domains, old)
	case "certificate":
		if !exists || r.CertPath == "" {
			return State{}, fmt.Errorf("更新证书需要受管域名及证书、私钥")
		}
	case "add", "import":
		if !exists {
			d = Domain{Name: name}
		}
	default:
		return State{}, fmt.Errorf("不支持的域名操作")
	}
	seen := map[string]bool{}
	for _, o := range d.Origins {
		seen[o] = true
	}
	for _, o := range r.Origins {
		v, e := NormalizeOrigin(o)
		if e != nil {
			return State{}, e
		}
		seen[v] = true
	}
	d.Origins = make([]string, 0, len(seen))
	for o := range seen {
		d.Origins = append(d.Origins, o)
	}
	sort.Strings(d.Origins)
	if r.CertPath != "" {
		d.CertPath = r.CertPath
		d.KeyPath = r.KeyPath
	}
	out.Domains[name] = d
	return out, nil
}
