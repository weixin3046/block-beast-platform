package httpapi

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type voidHTTPStub struct {
	AdminBetVoidService
	calls int
}

func (s *voidHTTPStub) VoidBet(_ context.Context, in operations.BetVoidInput) (operations.BetVoidResult, error) {
	s.calls++
	return operations.BetVoidResult{BetID: in.BetID, Currency: "POINTS", StakeMinor: 1500, RefundMinor: 1500, Status: "voided"}, nil
}
func TestAdminVoidPermissionPasswordAndAmount(t *testing.T) {
	for _, tt := range []struct {
		role        string
		passwordErr error
		want        int
	}{{"", nil, 401}, {"player", nil, 403}, {"operator", adminsecurity.ErrIncorrect, 401}, {"operator", nil, 200}, {"admin", nil, 200}} {
		stub := &voidHTTPStub{}
		sec := &securityStub{err: tt.passwordErr}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAdminSecurity(sec), WithAdminCompletion(stub, nil, nil))
		r := httptest.NewRequest("POST", "/v1/admin/bets/11111111-1111-4111-8111-111111111111/void", strings.NewReader(`{"request_id":"retry-key","reason":"测试","second_password":"secret"}`))
		if tt.role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{tt.role}))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Fatalf("role %s: %d %s", tt.role, w.Code, w.Body.String())
		}
		if tt.want != 200 && stub.calls != 0 {
			t.Fatal("unauthorized mutation")
		}
		if tt.want == 200 {
			var v map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
				t.Fatal(e)
			}
			if v["refund"] != "1.5" || v["stake"] != "1.5" || v["refund_minor"] != nil {
				t.Fatalf("amount ambiguity: %v", v)
			}
		}
	}
}
