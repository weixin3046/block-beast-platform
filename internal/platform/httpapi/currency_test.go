package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/currency"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubCurrencies struct {
	err   error
	calls int
}

func (s *stubCurrencies) List(context.Context, bool) ([]currency.Currency, error) {
	return []currency.Currency{}, s.err
}
func (s *stubCurrencies) Create(_ context.Context, c currency.Currency) (currency.Currency, error) {
	s.calls++
	return c, s.err
}
func (s *stubCurrencies) Update(context.Context, string, currency.Update) (currency.Currency, error) {
	s.calls++
	return currency.Currency{}, s.err
}

func TestCurrencyPermissionsAndRequests(t *testing.T) {
	valid := `{"code":"TEST","name":"测试","decimals":3,"category":"custom","enabled":true,"create_on_registration":false,"sort_order":1}`
	for _, tt := range []struct {
		role, body string
		err        error
		status     int
		calls      int
	}{
		{"", valid, nil, 401, 0}, {"player", valid, nil, 403, 0}, {"operator", valid, nil, 403, 0},
		{"admin", valid, nil, 201, 1}, {"admin", valid, currency.ErrConflict, 409, 1},
		{"admin", `{"code":"TEST","name":"测试"}`, nil, 400, 0},
		{"admin", valid + ` {}`, nil, 400, 0},
		{"admin", strings.Replace(valid, `"decimals":3`, `"decimals":3,"id":"ignored"`, 1), nil, 400, 0},
	} {
		stub := &stubCurrencies{err: tt.err}
		s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCurrencies(stub))
		r := httptest.NewRequest(http.MethodPost, "/v1/admin/currencies", strings.NewReader(tt.body))
		if tt.role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{tt.role}))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tt.status || stub.calls != tt.calls {
			t.Fatalf("%+v status %d calls %d", tt, w.Code, stub.calls)
		}
	}
}

type stubUnifiedLedger struct {
	CreditService
	user string
}

func (s *stubUnifiedLedger) ListUnifiedLedger(_ context.Context, user, _, _ string, _ int) (credit.LedgerPage, error) {
	s.user = user
	return credit.LedgerPage{Items: []credit.UnifiedLedgerEntry{}}, nil
}
func TestUnifiedLedgerUsesAuthenticatedUser(t *testing.T) {
	stub := &stubUnifiedLedger{}
	s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(stub))
	for _, tt := range []struct {
		url, token string
		status     int
	}{
		{"/v1/users/me/ledger", "", 401},
		{"/v1/users/me/ledger?account_id=other", issueTestToken(t, "my-user", []string{"player"}), 200},
		{"/v1/users/me/ledger?limit=101", issueTestToken(t, "my-user", []string{"player"}), 400},
	} {
		r := httptest.NewRequest("GET", tt.url, nil)
		if tt.token != "" {
			r.Header.Set("Authorization", "Bearer "+tt.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%s %d", tt.url, w.Code)
		}
	}
	if stub.user != "my-user" {
		t.Fatalf("user = %q", stub.user)
	}
}
