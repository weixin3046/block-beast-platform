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

type stubDepositCreditor struct {
	result chainapp.DepositResult
	err    error
}

func (stub stubDepositCreditor) CreditDeposit(_ context.Context, _ chainapp.DepositInput) (chainapp.DepositResult, error) {
	return stub.result, stub.err
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
	return request
}

func webhookServer(creditor DepositCreditor) *Server {
	return New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithChainDeposits(webhookSecret, 5*time.Minute, creditor))
}

func TestChainDepositWebhookVerifiesSignature(t *testing.T) {
	server := webhookServer(stubDepositCreditor{result: chainapp.DepositResult{DepositID: "d1", Status: "credited", Credited: true}})
	body := `{"provider_event_id":"e1","tx_hash":"t1","chain_code":"TRON","token_code":"USDT","address":"T1","amount_minor":100}`

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, signedWebhookRequest(t, body))
	if response.Code != http.StatusOK {
		t.Fatalf("valid signature status = %d, want 200", response.Code)
	}

	// 篡改正文后签名不再匹配。
	tampered := signedWebhookRequest(t, body)
	tampered.Body = io.NopCloser(strings.NewReader(`{"provider_event_id":"e2","tx_hash":"t2","chain_code":"TRON","token_code":"USDT","address":"T1","amount_minor":999}`))
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
	server.Handler().ServeHTTP(response, signedWebhookRequest(t, `{"provider_event_id":"e1","tx_hash":"t1","chain_code":"TRON","token_code":"USDT","address":"foreign","amount_minor":100}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ignored") {
		t.Fatalf("unknown address = %d %q, want 200 ignored", response.Code, response.Body.String())
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
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c1","destination_address":"T1","currency":"USDT","amount_minor":100}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", response.Code)
	}

	// 有令牌创建提现：身份取自令牌而非请求体。
	token := issueTestToken(t, "user-1", []string{identity.RolePlayer})
	request := httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c1","destination_address":"T1","currency":"USDT","amount_minor":100}`))
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
	request = httptest.NewRequest(http.MethodPost, "/v1/withdrawals", strings.NewReader(`{"client_request_id":"c2","destination_address":"T1","currency":"USDT","amount_minor":999999}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	insufficientServer.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("insufficient funds status = %d, want 409", response.Code)
	}
}
