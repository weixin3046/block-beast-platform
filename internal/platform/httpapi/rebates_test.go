package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/rebate"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

type rebateStub struct {
	RebateService
	calls int
}

func (s *rebateStub) ListConfigs(_ context.Context, q rebate.ConfigQuery) ([]rebate.Config, error) {
	s.calls++
	return []rebate.Config{{Currency: q.Currency, Levels: []rebate.Level{{Level: 1, RatePerMille: 14}}}}, nil
}
func TestRebateConfigRoles(t *testing.T) {
	for _, role := range []string{"", "player", "admin", "operator"} {
		stub := &rebateStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithRebates(stub))
		r := httptest.NewRequest("GET", "/v1/admin/rebate-configs?currency=POINTS", nil)
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 200
		if role == "" {
			want = 401
		}
		if role == "player" {
			want = 403
		}
		if w.Code != want {
			t.Fatal(role, w.Code, w.Body.String())
		}
		if want != 200 && stub.calls != 0 {
			t.Fatal("unauthorized call")
		}
	}
}
