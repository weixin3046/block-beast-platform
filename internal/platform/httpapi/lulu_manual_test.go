package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/application/externaldraw"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type manualDrawStub struct {
	calls int
	actor string
	err   error
}

func (s *manualDrawStub) ConfirmManual(_ context.Context, actor string, in externaldraw.ManualInput) (externaldraw.ManualResult, error) {
	s.calls++
	s.actor = actor
	return externaldraw.ManualResult{Game: in.Game, Issue: in.Issue, Result: in.Result, Status: "confirmed"}, s.err
}

func TestManualDrawRoute(t *testing.T) {
	for _, tc := range []struct {
		name, role, body         string
		passwordErr, errorResult error
		status, calls            int
	}{
		{name: "anonymous", status: 401}, {name: "player", role: "player", status: 403},
		{name: "missing password", role: "admin", body: `{}`, status: 400},
		{name: "bad password", role: "admin", passwordErr: adminsecurity.ErrIncorrect, status: 401},
		{name: "admin", role: "admin", status: 200, calls: 1}, {name: "operator", role: "operator", status: 200, calls: 1},
		{name: "conflict", role: "admin", errorResult: externaldraw.ErrManualConflict, status: 409, calls: 1},
		{name: "not found", role: "admin", errorResult: externaldraw.ErrManualNotFound, status: 404, calls: 1},
		{name: "invalid", role: "admin", errorResult: externaldraw.ErrInvalidEvent, status: 400, calls: 1},
		{name: "injected identity", role: "admin", body: `{"first_password":"secret","actor_user_id":"other"}`, status: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &manualDrawStub{err: tc.errorResult}
			pw := &securityStub{err: tc.passwordErr}
			s := New(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAdminSecurity(pw), WithLuluManualResults(stub))
			body := tc.body
			if body == "" {
				body = `{"first_password":"secret","game":"green_sprint","issue":"15693","result":[5],"reason":"manual verification"}`
			}
			r := httptest.NewRequest("POST", "/v1/admin/lulu/draw-results", strings.NewReader(body))
			if tc.role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{tc.role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || stub.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, stub.calls, w.Body.String())
			}
			if stub.calls > 0 && (stub.actor != "actor" || pw.level != "first") {
				t.Fatal("actor/password not enforced")
			}
		})
	}
}
