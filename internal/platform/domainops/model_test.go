package domainops

import (
	"reflect"
	"testing"
)

func TestNormalizeInputs(t *testing.T) {
	for _, input := range []string{"https://api.example.com", "a.example;", "a.example\nserver{}", "*.example.com", "a.example:443", "-a.example", "127.0.0.1", "foo", "a..com"} {
		if _, err := NormalizeDomain(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if got, err := NormalizeDomain("API.Example.com"); err != nil || got != "api.example.com" {
		t.Fatalf("%q %v", got, err)
	}
	for _, input := range []string{"https://a.example/path", "https://u:p@a.example", "https://*.example.com", "https://a.example?", "ftp://a.example", "https://a.example#x"} {
		if _, err := NormalizeOrigin(input); err == nil {
			t.Fatalf("accepted origin %q", input)
		}
	}
	if got, err := NormalizeOrigin("https://Web.Example.com:8443"); err != nil || got != "https://web.example.com:8443" {
		t.Fatalf("%q %v", got, err)
	}
}
func TestStateDoesNotMutateAndPreservesSharedOrigins(t *testing.T) {
	s := State{Version: 1, Domains: map[string]Domain{}}
	r := Request{Operation: "add", Domain: "a.example.com", Origins: []string{"https://web.example.com"}}
	next, err := NextState(s, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Domains) != 0 {
		t.Fatal("mutated original")
	}
	same, err := NextState(next, r)
	if err != nil || !reflect.DeepEqual(next, same) {
		t.Fatalf("not idempotent %v", err)
	}
	next, err = NextState(next, Request{Operation: "add", Domain: "b.example.com", Origins: r.Origins})
	if err != nil {
		t.Fatal(err)
	}
	next, err = NextState(next, Request{Operation: "remove", Domain: "a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ManagedOrigins(next); !reflect.DeepEqual(got, []string{"https://web.example.com"}) {
		t.Fatal(got)
	}
	if _, err = NextState(next, Request{Operation: "remove", Domain: "unknown.example.com"}); err == nil {
		t.Fatal("unknown remove accepted")
	}
	if _, err = NextState(next, Request{Operation: "add", Domain: "c.example.com", CertPath: "/cert"}); err == nil {
		t.Fatal("unpaired certificate accepted")
	}
}
