package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/config"
)

type securityStub struct {
	level string
	calls int
	err   error
}

func (s *securityStub) Status(context.Context, string) (adminsecurity.Status, error) {
	return adminsecurity.Status{FirstSet: true}, s.err
}
func (s *securityStub) Set(_ context.Context, _, level, login, password string) error {
	s.calls++
	s.level = level
	return s.err
}
func (s *securityStub) Verify(_ context.Context, _, level, password string) error {
	s.calls++
	s.level = level
	return s.err
}

func TestAdminSecurityRoles(t *testing.T) {
	for _, role := range []string{"admin", "operator", "player", ""} {
		for _, method := range []string{"GET", "PUT", "POST"} {
			stub := &securityStub{}
			s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAdminSecurity(stub))
			path := "/v1/admin/security-passwords"
			body := ""
			want := 200
			if method == "PUT" {
				path += "/first"
				body = `{"password":"new","login_password":"login"}`
				want = 204
			}
			if method == "POST" {
				path += "/second/verify"
				body = `{"password":"secret"}`
				want = 204
			}
			if role == "player" || (role == "operator" && method == "PUT") {
				want = 403
			}
			if role == "" {
				want = 401
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			if role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != want {
				t.Fatalf("%s %s: %d %s", role, method, w.Code, w.Body.String())
			}
			if want >= 400 && stub.calls != 0 {
				t.Fatal("unauthorized service invocation")
			}
		}
	}
}
func TestSensitiveConfigsRequireSecondPassword(t *testing.T) {
	for _, path := range []string{"/v1/admin/hash/config", "/v1/admin/configs/test", "/v1/admin/tasks/bet-configs", "/v1/admin/tasks/bet-configs/94000000-0000-4000-8000-000000000001", "/v1/admin/spins", "/v1/admin/spins/94000000-0000-4000-8000-000000000001", "/v1/admin/leaderboard-reward-rules"} {
		stub := &securityStub{err: adminsecurity.ErrLocked}
		s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAdminSecurity(stub))
		for _, body := range []string{`{}`, `{"first_password":"wrong-level"}`, `{"second_password":"secret"}`} {
			r := httptest.NewRequest("PUT", path, strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{"admin"}))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 400
			if strings.Contains(body, "second_password") {
				want = 429
				if w.Header().Get("Retry-After") != "900" || stub.level != "second" {
					t.Fatal("wrong level/lock response")
				}
			}
			if w.Code != want {
				t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
			}
		}
	}
}
func TestSecondPasswordRemovedBeforeBusinessHandler(t *testing.T) {
	stub := &securityStub{}
	s := &Server{adminSecurity: stub}
	called := false
	handler := s.secondPassword(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["second_password"]; ok {
			t.Fatal("secret reached business handler")
		}
		if string(body["value"]) != `{"limit":9007199254740993}` {
			t.Fatal("raw config precision changed")
		}
	})
	handler(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader(`{"second_password":"secret","value":{"limit":9007199254740993}}`)))
	if !called || stub.level != "second" {
		t.Fatal("not verified")
	}
}
