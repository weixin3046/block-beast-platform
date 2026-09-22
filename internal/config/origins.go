package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/block-beast/platform/internal/platform/domainops"
)

// LoadManagedOrigins merges only fully validated managed sources. Existing
// environment origins retain their semantics and cannot be removed by this file.
func (cfg *Config) LoadManagedOrigins() error {
	if cfg.ManagedOriginsFile == "" {
		return nil
	}
	f, err := os.Open(cfg.ManagedOriginsFile)
	if err != nil {
		return fmt.Errorf("read managed origins: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return fmt.Errorf("read managed origins: %w", err)
	}
	if len(data) > 1024*1024 {
		return fmt.Errorf("managed origins file exceeds 1 MiB")
	}
	var doc struct {
		Version int      `json:"version"`
		Origins []string `json:"origins"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&doc); err != nil {
		return fmt.Errorf("invalid managed origins JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || doc.Version != 1 {
		return fmt.Errorf("invalid managed origins version or trailing JSON")
	}
	api := append([]string(nil), cfg.APIAllowedOrigins...)
	ws := append([]string(nil), cfg.RealtimeAllowedOrigins...)
	appendUnique := func(dst []string, v string) []string {
		for _, old := range dst {
			if old == v {
				return dst
			}
		}
		return append(dst, v)
	}
	for _, origin := range doc.Origins {
		normalized, e := domainops.NormalizeOrigin(origin)
		if e != nil {
			return fmt.Errorf("invalid managed origin")
		}
		u, _ := url.Parse(normalized)
		api = appendUnique(api, normalized)
		ws = appendUnique(ws, u.Host)
	}
	cfg.APIAllowedOrigins = api
	cfg.RealtimeAllowedOrigins = ws
	return nil
}
