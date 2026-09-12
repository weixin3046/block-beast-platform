package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/config"
)

func (s *userControlStub) ConvertUserToVirtual(_ context.Context, actor string, id int64) error {
	if actor != "actor" || id != 10001 {
		return errors.New("wrong target")
	}
	s.calls++
	return nil
}

func TestConvertUserToVirtualHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, role, body, id string
		securityErr          error
		want                 int
	}{
		{"admin", "admin", `{"second_password":"secret"}`, "10001", nil, 200},
		{"operator", "operator", `{"second_password":"secret"}`, "10001", nil, 200},
		{"player", "player", `{"second_password":"secret"}`, "10001", nil, 403},
		{"anonymous", "", `{"second_password":"secret"}`, "10001", nil, 401},
		{"missing password", "admin", `{}`, "10001", nil, 400},
		{"wrong password", "admin", `{"second_password":"secret"}`, "10001", adminsecurity.ErrIncorrect, 401},
		{"unknown field", "admin", `{"second_password":"secret","is_virtual":false}`, "10001", nil, 400},
		{"bad id", "admin", `{"second_password":"secret"}`, "abc", nil, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &userControlStub{}
			security := &securityStub{err: tc.securityErr}
			s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithUserControls(stub), WithAdminSecurity(security))
			r := httptest.NewRequest("PUT", "/v1/admin/users/"+tc.id+"/virtual", strings.NewReader(tc.body))
			if tc.role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{tc.role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d %s want %d", w.Code, w.Body.String(), tc.want)
			}
			if tc.want == 200 {
				if !strings.Contains(w.Body.String(), `"is_virtual":true`) || !strings.Contains(w.Body.String(), `"user_id":10001`) || security.level != "second" {
					t.Fatal(w.Body.String())
				}
			} else if stub.calls != 0 {
				t.Fatal("rejected request mutated user")
			}
		})
	}
}
