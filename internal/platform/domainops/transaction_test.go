package domainops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingRunner struct {
	calls []string
	fail  string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	v := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, v)
	if r.fail != "" && strings.Contains(v, r.fail) {
		r.fail = ""
		return errors.New("injected failure")
	}
	return nil
}
func fixtureEngine(t *testing.T) (*Engine, *recordingRunner) {
	t.Helper()
	root := t.TempDir()
	r := &recordingRunner{}
	e := NewEngine(root, r)
	e.Probe = func(context.Context, Domain) (ProbeResult, error) {
		return ProbeResult{HTTP: "ok", HTTPS: "not-configured", WebSocket: "route-only"}, nil
	}
	for p, b := range map[string]string{nginxMain: "events {} http { include /www/server/panel/vhost/nginx/*.conf; }", envPath: "API_ALLOWED_ORIGINS=https://existing.example.com\n", capabilityPath: "1\n"} {
		f := e.path(p)
		os.MkdirAll(filepath.Dir(f), 0755)
		os.WriteFile(f, []byte(b), 0600)
	}
	os.MkdirAll(e.path(vhostDir), 0755)
	return e, r
}
func TestApplyHTTPIdempotentAndRemove(t *testing.T) {
	e, r := fixtureEngine(t)
	req := Request{Operation: "add", Domain: "api.example.com"}
	if err := e.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(e.path(vhostDir + "/block-beast-domain-api.example.com.conf"))
	if err != nil || !strings.Contains(string(data), "listen 80;") {
		t.Fatalf("%s %v", data, err)
	}
	if strings.Contains(strings.Join(r.calls, "\n"), "supervisorctl") {
		t.Fatal("restarted for route-only change")
	}
	count := len(r.calls)
	if err = e.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != count {
		t.Fatal("non-idempotent reload")
	}
	if err = e.Apply(context.Background(), Request{Operation: "remove", Domain: req.Domain}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(e.path(vhostDir + "/block-beast-domain-api.example.com.conf")); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}
func TestRollbackAndPendingRecovery(t *testing.T) {
	for _, fail := range []string{" -t ", " -s reload", " restart "} {
		t.Run(fail, func(t *testing.T) {
			e, r := fixtureEngine(t)
			r.fail = fail
			err := e.Apply(context.Background(), Request{Operation: "add", Domain: "api.example.com", Origins: []string{"https://web.example.com"}})
			if err == nil {
				t.Fatal("failure swallowed")
			}
			if _, err = os.Stat(e.path(vhostDir + "/block-beast-domain-api.example.com.conf")); !os.IsNotExist(err) {
				t.Fatal("configuration not rolled back")
			}
			if data, _ := os.ReadFile(e.path(envPath)); string(data) != "API_ALLOWED_ORIGINS=https://existing.example.com\n" {
				t.Fatalf("env not restored: %s", data)
			}
		})
	}
}
func TestProbeFailureRestoresOldDomain(t *testing.T) {
	e, _ := fixtureEngine(t)
	ctx := context.Background()
	if err := e.Apply(ctx, Request{Operation: "add", Domain: "old.example.com"}); err != nil {
		t.Fatal(err)
	}
	e.Probe = func(_ context.Context, d Domain) (ProbeResult, error) {
		if d.Name == "new.example.com" {
			if _, err := os.Stat(e.path(vhostDir + "/block-beast-domain-old.example.com.conf")); err != nil {
				t.Fatal("removed old before new verified")
			}
			return ProbeResult{}, errors.New("probe failure")
		}
		return ProbeResult{HTTP: "ok"}, nil
	}
	if err := e.Apply(ctx, Request{Operation: "replace", OldDomain: "old.example.com", Domain: "new.example.com"}); err == nil {
		t.Fatal("probe error swallowed")
	}
	if _, err := os.Stat(e.path(vhostDir + "/block-beast-domain-old.example.com.conf")); err != nil {
		t.Fatal(err)
	}
}
func TestDryRunAndConflict(t *testing.T) {
	e, r := fixtureEngine(t)
	p := e.path(vhostDir + "/other.conf")
	os.WriteFile(p, []byte(`server { listen 80; server_name api.example.com; }`), 0644)
	if e.Apply(context.Background(), Request{Operation: "add", Domain: "api.example.com"}) == nil {
		t.Fatal("duplicate accepted")
	}
	if err := e.Apply(context.Background(), Request{Operation: "add", Domain: "new.example.com", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 0 {
		t.Fatal("dry-run ran mutations")
	}
	if _, err := os.Stat(e.path(statePath)); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote state")
	}
}
