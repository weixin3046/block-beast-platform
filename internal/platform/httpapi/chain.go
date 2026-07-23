package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/application/audit"
	chainapp "github.com/block-beast/platform/internal/application/chain"
	chaindomain "github.com/block-beast/platform/internal/domain/chain"
	"github.com/block-beast/platform/internal/domain/wallet"
)

type DepositCreditor interface {
	CreditDeposit(ctx context.Context, input chainapp.DepositInput) (chainapp.DepositResult, error)
}

type WithdrawalService interface {
	RequestWithdrawal(ctx context.Context, input chainapp.WithdrawalInput) (chainapp.Withdrawal, error)
	FindWithdrawal(ctx context.Context, withdrawalID string) (chainapp.Withdrawal, error)
}

type DepositAddressReader interface {
	GetDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (chainapp.DepositAddress, error)
}

type chainWebhookConfig struct {
	secret   string
	skew     time.Duration
	creditor DepositCreditor
}

// WithChainDeposits 装配链上充值回调能力；secret 为空时端点返回 503。
func WithChainDeposits(secret string, skew time.Duration, creditor DepositCreditor) Option {
	return func(server *Server) {
		server.chainWebhook = &chainWebhookConfig{secret: secret, skew: skew, creditor: creditor}
	}
}

func WithWithdrawals(withdrawals WithdrawalService) Option {
	return func(server *Server) { server.withdrawals = withdrawals }
}

func WithDepositAddresses(addresses DepositAddressReader) Option {
	return func(server *Server) { server.depositAddresses = addresses }
}

// chainDepositWebhook 接收链上服务商的充值回调。
// 不依赖 JWT：请求通过 HMAC 签名（方法、路径、时间戳、随机数、正文哈希）验证来源，
// 时间戳超出允许偏移或签名不匹配一律 401。非平台地址返回 200 ignored 以终止服务商重试。
func (server *Server) chainDepositWebhook(writer http.ResponseWriter, request *http.Request) {
	if server.chainWebhook == nil || server.chainWebhook.secret == "" {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chain deposit webhook is unavailable"})
		return
	}
	rawBody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unable to read request body"})
		return
	}
	verifyErr := chaindomain.VerifyWebhook(
		server.chainWebhook.secret,
		request.Method,
		request.URL.Path,
		request.Header.Get("X-Timestamp"),
		request.Header.Get("X-Nonce"),
		rawBody,
		request.Header.Get("X-Signature"),
		time.Now().UTC(),
		server.chainWebhook.skew,
	)
	if errors.Is(verifyErr, chaindomain.ErrTimestampOutOfRange) || errors.Is(verifyErr, chaindomain.ErrInvalidSignature) {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": verifyErr.Error()})
		return
	}
	var input chainapp.DepositInput
	if err := json.Unmarshal(rawBody, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	result, err := server.chainWebhook.creditor.CreditDeposit(request.Context(), input)
	switch {
	case errors.Is(err, chainapp.ErrUnknownDepositAddress):
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ignored"})
		return
	case errors.Is(err, chainapp.ErrMissingFields), errors.Is(err, chainapp.ErrInvalidAmount):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to credit deposit"})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// requestWithdrawal 创建提现申请。用户身份以访问令牌为准；
// 鉴权关闭的本地开发模式下回退到请求体中的 account_id。
func (server *Server) requestWithdrawal(writer http.ResponseWriter, request *http.Request) {
	if server.withdrawals == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "withdrawals are unavailable"})
		return
	}
	var input struct {
		AccountID          string `json:"account_id"`
		ClientRequestID    string `json:"client_request_id"`
		DestinationAddress string `json:"destination_address"`
		Currency           string `json:"currency"`
		AmountMinor        int64  `json:"amount_minor"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	accountID := input.AccountID
	if claims, ok := ClaimsFromContext(request.Context()); ok {
		accountID = claims.Subject
	}
	if accountID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account is required"})
		return
	}
	withdrawal, err := server.withdrawals.RequestWithdrawal(request.Context(), chainapp.WithdrawalInput{
		UserID:             accountID,
		ClientRequestID:    input.ClientRequestID,
		DestinationAddress: input.DestinationAddress,
		Currency:           input.Currency,
		AmountMinor:        input.AmountMinor,
	})
	switch {
	case errors.Is(err, chainapp.ErrMissingFields), errors.Is(err, chainapp.ErrInvalidAmount):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, wallet.ErrWalletNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, wallet.ErrInsufficientFunds):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to request withdrawal"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{
		ActorUserID: accountID,
		Action:      "withdrawal.request",
		TargetType:  "withdrawal",
		TargetID:    withdrawal.WithdrawalID,
		Payload:     map[string]any{"currency": withdrawal.Currency, "amount_minor": withdrawal.AmountMinor, "status": withdrawal.Status},
	})
	writeJSON(writer, http.StatusCreated, withdrawal)
}

func (server *Server) depositAddress(writer http.ResponseWriter, request *http.Request) {
	if server.depositAddresses == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "deposit addresses are unavailable"})
		return
	}
	userID := ""
	if claims, ok := ClaimsFromContext(request.Context()); ok {
		userID = claims.Subject
	}
	if userID == "" {
		userID = request.URL.Query().Get("account_id")
	}
	chainCode, tokenCode := request.URL.Query().Get("chain_code"), request.URL.Query().Get("token_code")
	if userID == "" || chainCode == "" || tokenCode == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account_id, chain_code and token_code are required"})
		return
	}
	address, err := server.depositAddresses.GetDepositAddress(request.Context(), userID, chainCode, tokenCode)
	if errors.Is(err, chainapp.ErrDepositAddressNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read deposit address"})
		return
	}
	if !authorizeAccount(request, address.UserID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "address belongs to another account"})
		return
	}
	writeJSON(writer, http.StatusOK, address)
}

func (server *Server) withdrawal(writer http.ResponseWriter, request *http.Request) {
	if server.withdrawals == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "withdrawals are unavailable"})
		return
	}
	withdrawal, err := server.withdrawals.FindWithdrawal(request.Context(), request.PathValue("withdrawalID"))
	if errors.Is(err, chainapp.ErrWithdrawalNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read withdrawal"})
		return
	}
	if !authorizeAccount(request, withdrawal.UserID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "withdrawal belongs to another account"})
		return
	}
	writeJSON(writer, http.StatusOK, withdrawal)
}
