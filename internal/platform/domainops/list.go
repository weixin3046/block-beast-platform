package domainops

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

func (e *Engine) List(w io.Writer) error {
	if _, err := os.Stat(e.path(journalPath)); err == nil {
		return fmt.Errorf("存在未完成操作，不能将配置报告为已提交")
	}
	s, err := e.Load()
	if err != nil {
		return err
	}
	type item struct {
		Domain    string   `json:"domain"`
		HTTP      string   `json:"http"`
		HTTPS     string   `json:"https"`
		Expires   string   `json:"expires,omitempty"`
		Origins   []string `json:"origins"`
		LastCheck string   `json:"last_check"`
	}
	var last journal
	lastErr := readJSON(e.path(stateDir+"/last-operation.json"), &last)
	out := make([]item, 0, len(s.Domains))
	for _, d := range s.Domains {
		i := item{Domain: d.Name, HTTP: "configured", HTTPS: "not-configured", Origins: d.Origins, LastCheck: "unknown"}
		if lastErr == nil && last.Phase == "committed" {
			if _, ok := last.New.Domains[d.Name]; ok {
				i.LastCheck = "server-routes-verified; websocket-auth-not-recorded"
			}
		}
		if d.CertPath != "" {
			i.HTTPS = "configured"
			b, err := os.ReadFile(e.path(d.CertPath))
			if err != nil {
				return err
			}
			p, _ := pem.Decode(b)
			if p == nil {
				return fmt.Errorf("无法解析证书")
			}
			c, err := x509.ParseCertificate(p.Bytes)
			if err != nil {
				return err
			}
			i.Expires = c.NotAfter.UTC().Format(time.RFC3339)
			if time.Now().After(c.NotAfter) {
				i.HTTPS = "certificate-expired"
			}
		}
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
