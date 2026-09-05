package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type userControlStub struct {
	calls int
	kind  string
}

func (s *userControlStub) ResetUserPassword(_ context.Context, _ string, _ int64, kind, _ string) error {
	s.calls++
	s.kind = kind
	return nil
}
func (s *userControlStub) SetUserMuted(context.Context, string, int64, bool) error {
	s.calls++
	return nil
}
func (s *userControlStub) SearchUsers(context.Context, operations.UserSearch) ([]operations.User, error) {
	return []operations.User{}, nil
}
func TestUserControlPermissions(t *testing.T) {
	for _, kind := range []string{"password", "secondary-password", "mute"} {
		for _, role := range []string{"admin", "operator", "player", ""} {
			stub := &userControlStub{}
			security := &securityStub{}
			s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithUserControls(stub), WithAdminSecurity(security))
			body := `{"new_password":"test-password123","second_password":"global-secret"}`
			if kind == "mute" {
				body = `{"muted":true}`
			}
			r := httptest.NewRequest("PUT", "/v1/admin/users/100006/"+kind, strings.NewReader(body))
			if role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 204
			if role == "player" || (role == "operator" && kind != "mute") {
				want = 403
			}
			if role == "" {
				want = 401
			}
			if w.Code != want {
				t.Fatalf("%s %s %d %s", kind, role, w.Code, w.Body.String())
			}
			if want != 204 && stub.calls != 0 {
				t.Fatal("unauthorized write")
			}
			if want == 204 && kind != "mute" && security.level != "second" {
				t.Fatal("password not verified")
			}
			if want == 204 && kind == "secondary-password" && stub.kind != "secondary" {
				t.Fatal("wrong password kind")
			}
		}
	}
}
