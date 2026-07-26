package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	chainapp "github.com/block-beast/platform/internal/application/chain"
	"github.com/block-beast/platform/internal/config"
	chaindomain "github.com/block-beast/platform/internal/domain/chain"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/block-beast/platform/internal/domain/wallet"
)

const webhookSecret = "dev-webhook-secret-for-tests"
const webhookAPIKey = "test-api-key"

type stubDepositCreditor struct {
	result chainapp.DepositResult
	err    error
}

func (stub stubDepositCreditor) CreditPQPADeposit(_ context.Context, _ chainapp.PQPADepositWebhook) (chainapp.DepositResult, bool, error) {
	return stub.result, stub.err == nil, stub.err
}

func signedWebhookRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := "nonce-1"
	signature := chaindomain.SignWebhook(webhookSecret, http.MethodPost, "/v1/webhooks/chain/deposits", timestamp, nonce, []byte(body))
	request := httptest.NewRequest(http.MethodPost, "/v1/webhooks/chain/deposits", strings.NewReader(body))
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Nonce", nonce)
	request.Header.Set("X-Signature", signature)
	request.Header.Set("X-Api-Key", webhookAPIKey)
	return request
}

func webhookServer(creditor DepositCreditor) *Server {
	return New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithChainDeposits(webhookAPIKey, webhookSecret, 5*time.Minute, creditor))
}

func TestChainDepositWebhookVerifiesSignature(t *testing.T) {
	server := webhookServer(stubDepositCreditor{result: chainapp.DepositResult{DepositID: "d1", Status: "credited", Credited: true}})
	body := `{"event":"recharge","id":1,"chainTokenId":19,"address":"0x1","amount":"1.00","txHash":"0xabc","txStatus":"SETTLED"}`

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, signedWebhookRequest(t, body))
	if response.Code != http.StatusOK {
		t.Fatalf("valid signature status = %d, want 200", response.Code)
	}

	// 篡改正文后签名不再匹配。
	tampered := signedWebhookRequest(t, body)
	tampered.Body = io.NopCloser(strings.NewReader(`{"event":"recharge","id":2,"chainTokenId":19,"address":"0x1","amount":"9.99","txHash":"0xdef","txStatus":"SETTLED"}`))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, tampered)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered body status = %d, want 401", response.Code)
	}

	// 缺少签名头。
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/webhooks/chain/deposits", strings.NewReader(body)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature status = %d, want 401", response.Code)
	}
}

func TestChainDepositWebhookMapsResults(t *testing.T) {
	server := webhookServer(stubDepositCreditor{err: chainapp.ErrUnknownDepositAddress})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, signedWebhookRequest(t, `{"event":"recharge","id":1,"chainTokenId":19,"address":"foreign","amount":"1","txHash":"0x1","txStatus":"SETTLED"}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"code":0`) {
		t.Fatalf("unknown address = %d %q, want PQPA ACK", response.Code, response.Body.String())
	}

	unconfigured := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)
	response = httptest.NewRecorder()
	unconfigured.Handler().ServeHTTP(response, signedWebhookRequest(t, `{}`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured webhook status = %d, want 503", response.Code)
	}
}

type stubWithdrawalService struct {
	requestResult chainapp.Withdrawal
	requestErr    error
	findResult    chainapp.Withdrawal
	findErr       error
}

func (stub stubWithdrawalService) RequestWithdrawal(_ context.Context, _ chainapp.WithdrawalInput) (chainapp.Withdrawal, error) {
	return stub.requestResult, stub.requestErr
}

func (stub stubWithdrawalService) FindWithdrawal(_ context.Context, _ string) (chainapp.Withdrawal, error) {
	return stub.findResult, stub.findErr
}

func (stub stubWithdrawalService) ApproveWithdrawal(_ context.Context, _ string, _ string) (chainapp.Withdrawal, error) {
	return stub.findResult, nil
}

func (stub stubWithdrawalService) RejectWithdrawal(_ context.Context, _ string, _ string, _ string) (chainapp.Withdrawal, error) {
	return stub.findResult, nil
}

func (stub stubWithdrawalService) ListWithdrawals(_ context.Context, _ string, _ int) ([]chainapp.Withdrawal, error) {
	return []chainapp.Withdrawal{stub.findResult}, nil
}

func (stub stubWithdrawalService) ListUserWithdrawals(_ context.Context, _ string, _ int) ([]chainapp.Withdrawal, error) {
	return []chainapp.Withdrawal{stub.findResult}, nil
}

func TestWithdrawalEndpointsRequireAuthAndOwnership(t *testing.T) {
	authenticator := NewAuthenticator(testSecret)
	stub := stubWithdrawalService{
		requestResult: chainapp.Withdrawal{WithdrawalID: "w1", UserID: "user-1", Status: "requested"},
		findResult:    chainapp.Withdrawal{WithdrawalID: "w1", UserID: "user-1", Status: "requested"},
	}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(authenticator), WithWithdrawals(stub))

	// 无令牌创建提现。
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c1","destination_address":"0x1","chain_code":"POLYGON","currency":"USDT","amount_minor":100}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", response.Code)
	}

	// 有令牌创建提现：身份取自令牌而非请求体。
	token := issueTestToken(t, "user-1", []string{identity.RolePlayer})
	request := httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c1","destination_address":"0x1","chain_code":"POLYGON","currency":"USDT","amount_minor":100}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("with token status = %d, want 201", response.Code)
	}

	// 他人提现记录 403；本人 200。
	otherToken := issueTestToken(t, "user-2", []string{identity.RolePlayer})
	request = httptest.NewRequest(http.MethodGet, "/v1/withdrawals/w1", nil)
	request.Header.Set("Authorization", "Bearer "+otherToken)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("other account status = %d, want 403", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/withdrawals/w1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want 200", response.Code)
	}

	// 余额不足映射 409。
	stub.requestErr = wallet.ErrInsufficientFunds
	insufficientServer := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(authenticator), WithWithdrawals(stub))
	request = httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c2","destination_address":"0x1","chain_code":"POLYGON","currency":"USDT","amount_minor":999999}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	insufficientServer.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("insufficient funds status = %d, want 409", response.Code)
	}
}

func TestWithdrawalEndpointMapsRiskErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid address", err: chainapp.ErrInvalidWithdrawalAddress, want: http.StatusBadRequest},
		{name: "below minimum", err: chainapp.ErrWithdrawalBelowMinimum, want: http.StatusBadRequest},
		{name: "above maximum", err: chainapp.ErrWithdrawalAboveMaximum, want: http.StatusBadRequest},
		{name: "daily limit", err: chainapp.ErrWithdrawalDailyLimit, want: http.StatusConflict},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := New(
				config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
				nil, readinessChecker{}, nil, nil, nil, nil,
				WithAuth(NewAuthenticator(testSecret)),
				WithWithdrawals(stubWithdrawalService{requestErr: testCase.err}),
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(
				`{"client_request_id":"c1","destination_address":"0x1111111111111111111111111111111111111111","chain_code":"POLYGON","currency":"USDT","amount_minor":100}`,
			))
			request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d", response.Code, testCase.want)
			}
		})
	}
}
