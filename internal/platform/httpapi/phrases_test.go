package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
)

type phraseStub struct {
	PhraseService
	calls int
}

func (s *phraseStub) ListPhrases(context.Context, string, operations.PhraseFilter) (operations.PhrasePage, error) {
	s.calls++
	return operations.PhrasePage{Items: []operations.Phrase{}}, nil
}
func (s *phraseStub) SavePhrase(context.Context, string, int64, string, operations.PhraseInput) (operations.Phrase, error) {
	s.calls++
	return operations.Phrase{}, nil
}
func (s *phraseStub) DeletePhrase(context.Context, string, int64) error { s.calls++; return nil }
func (s *phraseStub) SetPhraseEnabled(context.Context, string, int64, bool) (operations.Phrase, error) {
	s.calls++
	return operations.Phrase{}, nil
}
func (s *phraseStub) ReorderPhrases(context.Context, string, []int64) ([]operations.Phrase, error) {
	s.calls++
	return []operations.Phrase{}, nil
}
func TestPhraseRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v1/admin/phrases", "", 200}, {"POST", "/v1/admin/phrases", `{"request_id":"key","title":"hi","content":"hello"}`, 200}, {"PUT", "/v1/admin/phrases/1", `{"title":"hi","content":"hello"}`, 200}, {"DELETE", "/v1/admin/phrases/1", "", 204}, {"PUT", "/v1/admin/phrases/1/enabled", `{"enabled":false}`, 200}, {"PUT", "/v1/admin/phrases/order", `{"ids":[]}`, 200},
		{"GET", "/v1/admin/phrases?enabled=1", "", 400}, {"GET", "/v1/admin/phrases?page=1.5", "", 400}, {"PUT", "/v1/admin/phrases/1/enabled", `{}`, 400}, {"PUT", "/v1/admin/phrases/1/enabled", `{"enabled":"true"}`, 400}, {"PUT", "/v1/admin/phrases/order", `{"ids":["1"]}`, 400}, {"PUT", "/v1/admin/phrases/0", `{}`, 400}, {"POST", "/v1/admin/phrases", `{"content":"a","unknown":1}`, 400}, {"POST", "/v1/admin/phrases", `{} {}`, 400},
	} {
		for _, role := range []string{"admin", "operator", "player", ""} {
			stub := &phraseStub{}
			s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithPhrases(stub))
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := tc.status
			if role != "admin" && role != "operator" {
				want = 403
			}
			if role == "" {
				want = 401
			}
			if w.Code != want {
				t.Fatalf("%s %s %s: %d %s", role, tc.method, tc.path, w.Code, w.Body.String())
			}
			if want >= 400 && stub.calls != 0 {
				t.Fatal("invalid call reached service")
			}
		}
	}
}
