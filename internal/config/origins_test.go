package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManagedOriginsMergeAndFailClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "origins.json")
	cfg := Config{APIAllowedOrigins: []string{"https://existing.example.com"}, RealtimeAllowedOrigins: []string{"existing.example.com"}}
	if err := cfg.LoadManagedOrigins(); err != nil {
		t.Fatal(err)
	}
	cfg.ManagedOriginsFile = p
	for _, body := range []string{"", `{"version":2,"origins":[]}`, `{"version":1,"origins":["*"]}`, `{"version":1,"origins":[]} {}`, `{"version":1,"unknown":true}`} {
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := cfg.LoadManagedOrigins(); err == nil {
			t.Fatalf("accepted %q", body)
		}
		if len(cfg.APIAllowedOrigins) != 1 {
			t.Fatal("partially changed configuration")
		}
	}
	os.Remove(p)
	if cfg.LoadManagedOrigins() == nil {
		t.Fatal("missing explicit file accepted")
	}
	os.WriteFile(p, []byte(`{"version":1,"origins":["https://web.example.com","http://web.example.com","https://existing.example.com"]}`), 0600)
	if err := cfg.LoadManagedOrigins(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.APIAllowedOrigins, []string{"https://existing.example.com", "https://web.example.com", "http://web.example.com"}) {
		t.Fatal(cfg.APIAllowedOrigins)
	}
	if !reflect.DeepEqual(cfg.RealtimeAllowedOrigins, []string{"existing.example.com", "web.example.com"}) {
		t.Fatal(cfg.RealtimeAllowedOrigins)
	}
}
