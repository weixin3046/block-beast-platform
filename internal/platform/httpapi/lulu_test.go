package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/adminsecurity"
	"github.com/block-beast/platform/internal/application/lulu"
	"github.com/block-beast/platform/internal/config"
)

type luluStub struct {
	LuluService
	calls int
	actor string
}

func (s *luluStub) Create(_ context.Context, user, kind string, in lulu.Input) (lulu.Order, error) {
	s.calls++
	s.actor = user
	return lulu.Order{ID: "test", UserID: user, Kind: kind, Currency: "ORIGIN_STONE", Amount: in.Amount}, nil
}
func (s *luluStub) Review(_ context.Context, id, actor, action, evidence string) (lulu.Order, error) {
	s.calls++
	s.actor = actor
	return lulu.Order{ID: id, Currency: "ORIGIN_STONE", Amount: "1", Status: "approved"}, nil
}

func TestLuluCreationRequiresSessionAndRejectsInjectedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body, role string
		auth             bool
		status, calls    int
	}{
		{"anonymous even in development", `{"request_id":"one","lulu_uid":"1234567","amount":"1"}`, "", false, 401, 0},
		{"player", `{"request_id":"one","lulu_uid":"1234567","amount":"1"}`, "player", true, 201, 1},
		{"cannot inject user", `{"request_id":"one","lulu_uid":"1234567","amount":"1","user_id":"someone"}`, "player", true, 400, 0},
		{"integer must be string", `{"request_id":"one","lulu_uid":"1234567","amount":1}`, "player", true, 400, 0},
		{"trailing body", `{"request_id":"one","lulu_uid":"1234567","amount":"1"} {}`, "player", true, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &luluStub{}
			opts := []Option{WithLulu(stub)}
			if tc.auth {
				opts = append(opts, WithAuth(NewAuthenticator(testSecret)))
			}
			s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, opts...)
			req := httptest.NewRequest("POST", "/v1/lulu/deposits", strings.NewReader(tc.body))
			if tc.role != "" {
				req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-user", []string{tc.role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != tc.status || stub.calls != tc.calls {
				t.Fatalf("%d calls %d: %s", w.Code, stub.calls, w.Body.String())
			}
			if stub.calls > 0 && stub.actor != "player-user" {
				t.Fatal("actor must come from token")
			}
		})
	}
}
func TestLuluReviewRequiresRoleAndFundsPassword(t *testing.T) {
	for _, tc := range []struct {
		role          string
		passwordErr   error
		status, calls int
	}{
		{"player", nil, 403, 0}, {"admin", adminsecurity.ErrIncorrect, 401, 0}, {"admin", nil, 200, 1}, {"operator", nil, 200, 1},
	} {
		stub := &luluStub{}
		pw := &fundsPasswordStub{err: tc.passwordErr}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub), WithAdminSecurity(pw))
		req := httptest.NewRequest("POST", "/v1/admin/lulu/orders/123/review", strings.NewReader(`{"action":"approve","evidence":"checked","first_password":"test-secret"}`))
		req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "reviewer", []string{tc.role}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != tc.status || stub.calls != tc.calls {
			t.Fatalf("%s: %d calls %d %s", tc.role, w.Code, stub.calls, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "test-secret") {
			t.Fatal("password leaked")
		}
	}
}

func (s *luluStub) Config(context.Context) (lulu.Config, error) {
	return lulu.Config{ReceiverUID: "7654321", Enabled: true, Version: 3}, nil
}
func (s *luluStub) UpdateConfig(_ context.Context, actor string, in lulu.ConfigUpdate) (lulu.Config, error) {
	s.calls++
	s.actor = actor
	if in.ReceiverUID != "7654321" {
		return lulu.Config{}, lulu.ErrConfigInvalid
	}
	return lulu.Config{ReceiverUID: in.ReceiverUID, Enabled: in.Enabled, Version: in.Version + 1}, nil
}

type luluConfigPasswordStub struct {
	AdminSecurityService
	level string
	err   error
}

func (s *luluConfigPasswordStub) Verify(_ context.Context, actor, level, password string) error {
	s.level = level
	return s.err
}
func TestLuluConfigAuthorizationAndPassword(t *testing.T) {
	for _, tc := range []struct {
		name, role, body string
		passwordErr      error
		status, calls    int
	}{
		{"admin", "admin", `{"enabled":true,"version":1,"second_password":"test-secret"}`, nil, 200, 1},
		{"operator", "operator", `{"enabled":true,"version":1,"second_password":"test-secret"}`, nil, 200, 1},
		{"player", "player", `{"enabled":true,"version":1,"second_password":"test-secret"}`, nil, 403, 0},
		{"wrong password", "admin", `{"enabled":true,"version":1,"second_password":"test-secret"}`, adminsecurity.ErrIncorrect, 401, 0},
		{"missing enabled", "admin", `{"version":1,"second_password":"test-secret"}`, nil, 400, 0},
		{"receiver uid rejected", "admin", `{"receiver_uid":"1234567","enabled":true,"version":1,"second_password":"test-secret"}`, nil, 400, 0},
		{"unknown field", "admin", `{"enabled":true,"version":1,"second_password":"test-secret","token":"forbidden"}`, nil, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &luluStub{}
			pw := &luluConfigPasswordStub{err: tc.passwordErr}
			s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub), WithAdminSecurity(pw))
			req := httptest.NewRequest("PUT", "/v1/admin/lulu/config", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{tc.role}))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != tc.status || stub.calls != tc.calls {
				t.Fatalf("%d calls=%d %s", w.Code, stub.calls, w.Body.String())
			}
			if stub.calls > 0 && (pw.level != "second" || stub.actor != "admin-user") {
				t.Fatal("wrong authentication context")
			}
			if strings.Contains(w.Body.String(), "test-secret") {
				t.Fatal("password exposed")
			}
		})
	}
}
func TestLuluPlayerConfigReadsDatabaseService(t *testing.T) {
	s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(&luluStub{}))
	req := httptest.NewRequest("GET", "/v1/lulu/config", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-user", []string{"player"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"receiver_uid":"7654321"`) {
		t.Fatalf("database config not exposed: %d %s", w.Code, w.Body.String())
	}
}

func (s *luluStub) SendLoginCode(context.Context, string, string, int64) error { s.calls++; return nil }
func (s *luluStub) PhoneLogin(context.Context, string, string, string, int64) (lulu.Config, error) {
	s.calls++
	return lulu.Config{ReceiverUID: "1234567", TokenConfigured: true, Version: 2}, nil
}
func TestLuluSMSRoutesRequireAdminAndSecondPassword(t *testing.T) {
	for _, path := range []string{"/v1/admin/lulu/send-code", "/v1/admin/lulu/login"} {
		for _, role := range []string{"player", "operator", "admin"} {
			stub := &luluStub{}
			pw := &luluConfigPasswordStub{}
			s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub), WithAdminSecurity(pw))
			body := `{"phone":"13800000000","version":1,"second_password":"test-secret"}`
			if strings.HasSuffix(path, "/login") {
				body = `{"phone":"13800000000","code":"123456","version":1,"second_password":"test-secret"}`
			}
			req := httptest.NewRequest("POST", path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{role}))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if role == "admin" || role == "operator" {
				if w.Code != 200 || stub.calls != 1 || pw.level != "second" {
					t.Fatalf("admin %s: %d %s", path, w.Code, w.Body.String())
				}
			} else if w.Code != 403 || stub.calls != 0 {
				t.Fatal("non-admin accepted")
			}
			if strings.Contains(w.Body.String(), "test-secret") || strings.Contains(w.Body.String(), "123456\"") {
				t.Fatal("SMS secrets exposed")
			}
		}
	}
}

func TestLuluConfigUsesOriginStone(t *testing.T) {
	s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(&luluStub{}))
	req := httptest.NewRequest("GET", "/v1/lulu/config", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-user", []string{"player"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"currency":"ORIGIN_STONE"`) || !strings.Contains(w.Body.String(), `"decimals":3`) {
		t.Fatalf("wrong wallet currency/precision: %d %s", w.Code, w.Body.String())
	}
}

func TestLuluSendCodeRejectsCodeField(t *testing.T) {
	for _, code := range []string{`""`, `null`, `"123456"`} {
		stub := &luluStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub), WithAdminSecurity(&luluConfigPasswordStub{}))
		req := httptest.NewRequest("POST", "/v1/admin/lulu/send-code", strings.NewReader(`{"phone":"13800000000","version":1,"second_password":"test-secret","code":`+code+`}`))
		req.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-user", []string{"admin"}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != 400 || stub.calls != 0 {
			t.Fatalf("code %s: %d calls=%d", code, w.Code, stub.calls)
		}
	}
}

func TestLuluSameAccountErrorMessage(t *testing.T) {
	w := httptest.NewRecorder()
	if !luluError(w, lulu.ErrSameAccount) || w.Code != 400 || !strings.Contains(w.Body.String(), "玩家噜噜账号不能与平台收付账号相同") {
		t.Fatalf("unexpected error response: %d %s", w.Code, w.Body.String())
	}
}

func TestPendingDepositDisplaysUIDAndCountdown(t *testing.T) {
	w := httptest.NewRecorder()
	luluError(w, &lulu.PendingDepositError{LuluUID: "57870217", RetryAfterSeconds: 125})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "噜噜账号 57870217") || !strings.Contains(w.Body.String(), "等待 125 秒") || !strings.Contains(w.Body.String(), `"retry_after_seconds":125`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func (s *luluStub) Transfers(context.Context, string, string, int, int) (lulu.TransferPage, error) {
	s.calls++
	return lulu.TransferPage{Items: []lulu.TransferRecord{}}, nil
}
func TestTransferListStaffOnly(t *testing.T) {
	for _, role := range []string{"player", "admin", "operator"} {
		stub := &luluStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub))
		r := httptest.NewRequest("GET", "/v1/admin/lulu/transfers?direction=sent", nil)
		r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "staff", []string{role}))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 200
		if role == "player" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("%s %d", role, w.Code)
		}
	}
}

func (s *luluStub) AccountBalance(context.Context, string) (lulu.AccountBalance, error) {
	return lulu.AccountBalance{ReceiverUID: "1234567", ItemID: 102201, Balance: "5160.07704"}, nil
}

func TestBalanceStaffOnly(t *testing.T) {
	for _, role := range []string{"", "player", "admin", "operator"} {
		stub := &luluStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLulu(stub))
		r := httptest.NewRequest("GET", "/v1/admin/lulu/balance", nil)
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "staff", []string{role}))
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
			t.Fatalf("%s %d", role, w.Code)
		}
	}
}
