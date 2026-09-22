package domainops

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestInterruptedJournalRestoredBeforeNextOperation(t *testing.T) {
	e, _ := fixtureEngine(t)
	ctx := context.Background()
	req := Request{Operation: "add", Domain: "api.example.com"}
	if err := e.Apply(ctx, req); err != nil {
		t.Fatal(err)
	}
	var j journal
	if err := readJSON(e.path(stateDir+"/last-operation.json"), &j); err != nil {
		t.Fatal(err)
	}
	j.Phase = "applied"
	if err := writeJSON(e.path(journalPath), j); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(ctx, Request{Operation: "add", Domain: "other.example.com"}); err != nil {
		t.Fatal(err)
	}
	s, err := e.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Domains["api.example.com"]; ok {
		t.Fatal("interrupted state not restored")
	}
	if _, ok := s.Domains["other.example.com"]; !ok {
		t.Fatal("new operation missing")
	}
}
func TestManagedSourcesSharedAndEnvironmentPreserved(t *testing.T) {
	e, r := fixtureEngine(t)
	ctx := context.Background()
	for _, n := range []string{"a.example.com", "b.example.com"} {
		if err := e.Apply(ctx, Request{Operation: "add", Domain: n, Origins: []string{"https://web.example.com"}}); err != nil {
			t.Fatal(err)
		}
	}
	calls := len(r.calls)
	if err := e.Apply(ctx, Request{Operation: "remove", Domain: "a.example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(r.calls[calls:], "\n"), "restart") {
		t.Fatal("shared origin removal restarted")
	}
	var doc struct {
		Version int      `json:"version"`
		Origins []string `json:"origins"`
	}
	if err := readJSON(e.path(originsPath), &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Origins, []string{"https://web.example.com"}) {
		t.Fatal(doc)
	}
	b, _ := os.ReadFile(e.path(envPath))
	if !strings.Contains(string(b), "https://existing.example.com") {
		t.Fatal("existing env removed")
	}
}
func TestTLSUploadAndRepeatNoReload(t *testing.T) {
	e, r := fixtureEngine(t)
	c, k, roots, _ := testCertificate(t)
	e.Roots = roots
	os.WriteFile(e.path("/cert.pem"), c, 0600)
	os.WriteFile(e.path("/key.pem"), k, 0600)
	req := Request{Operation: "add", Domain: "api.example.com", CertPath: "/cert.pem", KeyPath: "/key.pem"}
	if err := e.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before := len(r.calls)
	if err := e.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != before {
		t.Fatal("repeated identical certificate reloaded")
	}
}

type changingRunner struct {
	e       *Engine
	changed bool
}

func (r *changingRunner) Run(_ context.Context, _ string, args ...string) error {
	if !r.changed && len(args) > 0 && args[0] == "-t" {
		r.changed = true
		return os.WriteFile(r.e.path(envPath), []byte("EXTERNAL=changed\n"), 0640)
	}
	return nil
}
func TestExternalEditDuringPreflightIsNotOverwritten(t *testing.T) {
	e, _ := fixtureEngine(t)
	e.Runner = &changingRunner{e: e}
	err := e.Apply(context.Background(), Request{Operation: "add", Domain: "api.example.com", Origins: []string{"https://web.example.com"}})
	if err == nil {
		t.Fatal("concurrent edit accepted")
	}
	b, _ := os.ReadFile(e.path(envPath))
	if string(b) != "EXTERNAL=changed\n" {
		t.Fatalf("external edit overwritten: %s", b)
	}
}
func TestLockPreventsConcurrentApply(t *testing.T) {
	e, _ := fixtureEngine(t)
	lock, err := e.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if e.Apply(context.Background(), Request{Operation: "add", Domain: "api.example.com"}) == nil {
		t.Fatal("ignored held lock")
	}
}

func TestInterruptedRecoveryPreservesManualRepair(t *testing.T) {
	e, _ := fixtureEngine(t)
	ctx := context.Background()
	if err := e.Apply(ctx, Request{Operation: "add", Domain: "api.example.com"}); err != nil {
		t.Fatal(err)
	}
	var j journal
	if err := readJSON(e.path(stateDir+"/last-operation.json"), &j); err != nil {
		t.Fatal(err)
	}
	j.Phase = "applied"
	writeJSON(e.path(journalPath), j)
	p := e.configPath("api.example.com")
	os.WriteFile(p, []byte("# manual repair\n"), 0644)
	if err := e.Apply(ctx, Request{Operation: "add", Domain: "other.example.com"}); err == nil {
		t.Fatal("manual repair conflict accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "# manual repair\n" {
		t.Fatal("manual repair overwritten")
	}
	if _, err := os.Stat(e.path(journalPath)); err != nil {
		t.Fatal("recovery evidence removed")
	}
}
