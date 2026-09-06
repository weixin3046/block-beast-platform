package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/config"
)

type adjustmentStub struct {
	CreditService
	calls int
	input credit.AdjustmentInput
	err   error
}

func (s *adjustmentStub) AdjustWallet(_ context.Context, in credit.AdjustmentInput) (credit.AdjustmentResult, error) {
	s.calls++
	s.input = in
	return credit.AdjustmentResult{Currency:"POINTS",UserID: in.UserID, Action: in.Action}, s.err
}
func (s *adjustmentStub) AdminCredit(ctx context.Context, in credit.AdminCreditInput) (credit.CreditResult, error) {
	_, err := s.AdjustWallet(ctx, credit.AdjustmentInput{OperatorID: in.OperatorID, UserID: in.UserID, Action: "credit"})
	return credit.CreditResult{Currency:"POINTS"}, err
}

type fundsPasswordStub struct {
	AdminSecurityService
	err            error
	user, password string
}

func (s *fundsPasswordStub) Verify(_ context.Context, user, level, password string) error {
	s.user = user
	s.password = password
	return s.err
}

func TestAdminFundsEndpointsPermissionsPasswordsAndErrors(t *testing.T) {
	for _, path := range []string{"/v1/admin/credits", "/v1/admin/wallet-adjustments"} {
		body := `{"user_id":"100009","currency":"POINTS","amount":"1.5","request_id":"one","first_password":"secret"}`
		if strings.HasSuffix(path, "adjustments") {
			body = strings.TrimSuffix(body, "}") + `,"action":"debit"}`
		}
		for _, tc := range []struct {
			name, role               string
			passwordErr, businessErr error
			status, calls            int
		}{
			{"anonymous", "", nil, nil, 401, 0}, {"player", "player", nil, nil, 403, 0}, {"operator", "operator", nil, nil, 200, 1},
			{"password missing setup", "admin", adminsecurity.ErrNotSet, nil, 409, 0},
			{"wrong password", "admin", adminsecurity.ErrIncorrect, nil, 401, 0},
			{"success", "admin", nil, nil, 200, 1},
			{"insufficient", "admin", nil, credit.ErrInsufficientBalance, 409, 1},
			{"virtual", "admin", nil, credit.ErrVirtualAccountWithdrawal, 403, 1},
			{"conflict", "admin", nil, credit.ErrAdjustmentConflict, 409, 1},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				credits := &adjustmentStub{err: tc.businessErr}
				passwords := &fundsPasswordStub{err: tc.passwordErr}
				s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(credits), WithAdminSecurity(passwords))
				r := httptest.NewRequest("POST", path, strings.NewReader(body))
				if tc.role != "" {
					r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{tc.role}))
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != tc.status || credits.calls != tc.calls {
					t.Fatalf("status=%d calls=%d body=%s", w.Code, credits.calls, w.Body.String())
				}
				if credits.calls > 0 && (credits.input.OperatorID != "admin-user" || passwords.user != "admin-user" || passwords.password != "secret") {
					t.Fatal("must verify and inject operator, not target user")
				}
				if strings.Contains(w.Body.String(), "secret") {
					t.Fatal("password leaked")
				}
			})
		}
		for _, invalid := range []string{strings.Replace(body, `,"first_password":"secret"`, "", 1), strings.Replace(body, `"100009"`, `100009`, 1), body + ` {}`, strings.TrimSuffix(body, "}") + `,"operator_id":"other"}`} {
			c := &adjustmentStub{}
			p := &fundsPasswordStub{}
			s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(c), WithAdminSecurity(p))
			r := httptest.NewRequest("POST", path, strings.NewReader(invalid))
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{"admin"}))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 400 || c.calls != 0 {
				t.Fatalf("invalid %s: %d %s", invalid, w.Code, w.Body.String())
			}
		}
	}
}
