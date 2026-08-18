package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/application/audit"
	chainapp "github.com/block-beast/platform/internal/application/chain"
)

type DepositCreditor interface {
	CreditPQPADeposit(ctx context.Context, input chainapp.PQPADepositWebhook) (chainapp.DepositResult, bool, error)
}

type DepositReader interface {
	ListDeposits(ctx context.Context, userID string, limit int) ([]chainapp.Deposit, error)
}

type WithdrawalStatusApplier interface {
	ApplyWithdrawalStatus(ctx context.Context, input chainapp.WithdrawalStatusInput) error
	ApplyPQPAWithdrawal(ctx context.Context, input chainapp.PQPAWithdrawalWebhook) (bool, error)
}

type WithdrawalService interface {
	RequestWithdrawal(ctx context.Context, input chainapp.WithdrawalInput) (chainapp.Withdrawal, error)
	FindWithdrawal(ctx context.Context, withdrawalID string) (chainapp.Withdrawal, error)
	ApproveWithdrawal(ctx context.Context, withdrawalID, reviewerID string) (chainapp.Withdrawal, error)
	RejectWithdrawal(ctx context.Context, withdrawalID, reviewerID, reason string) (chainapp.Withdrawal, error)
	ListWithdrawals(ctx context.Context, status string, limit int) ([]chainapp.Withdrawal, error)
	ListUserWithdrawals(ctx context.Context, userID string, limit int) ([]chainapp.Withdrawal, error)
}

func WithDepositHistory(reader DepositReader) Option {
	return func(server *Server) { server.depositHistory = reader }
}

func (server *Server) userDeposits(writer http.ResponseWriter, request *http.Request) {
	if server.depositHistory == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "deposit history is unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	userID := request.URL.Query().Get("account_id")
	if ok {
		userID = claims.Subject
	} else if userID != "" {
		internalID, err := server.resolvePublicUserID(request.Context(), userID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		userID = internalID
	}
	if userID == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	items, err := server.depositHistory.ListDeposits(request.Context(), userID, 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list deposits"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, items)
}

func (server *Server) userWithdrawals(writer http.ResponseWriter, request *http.Request) {
	claims, ok := ClaimsFromContext(request.Context())
	userID := request.URL.Query().Get("account_id")
	if ok {
		userID = claims.Subject
	} else if userID != "" {
		internalID, err := server.resolvePublicUserID(request.Context(), userID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		userID = internalID
	}
	if userID == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	items, err := server.withdrawals.ListUserWithdrawals(request.Context(), userID, 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list withdrawals"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, items)
}

func (server *Server) adminWithdrawals(writer http.ResponseWriter, request *http.Request) {
	items, err := server.withdrawals.ListWithdrawals(request.Context(), request.URL.Query().Get("status"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list withdrawals"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, items)
}

func (server *Server) rejectWithdrawal(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input)
	claims, _ := ClaimsFromContext(request.Context())
	withdrawal, err := server.withdrawals.RejectWithdrawal(request.Context(), request.PathValue("withdrawalID"), claims.Subject, input.Reason)
	switch {
	case errors.Is(err, chainapp.ErrWithdrawalNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, chainapp.ErrWithdrawalState):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to reject withdrawal"})
	default:
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "withdrawal.reject", TargetType: "withdrawal", TargetID: withdrawal.WithdrawalID, Payload: map[string]any{"reason": input.Reason}})
		server.writePublicJSON(writer, request, http.StatusOK, withdrawal)
	}
}

type DepositAddressReader interface {
	GetDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (chainapp.DepositAddress, error)
}

type DepositAddressService interface {
	DepositAddressReader
	CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (chainapp.DepositAddress, error)
}

type chainWebhookConfig struct {
	apiKey      string
	secret      string
	skew        time.Duration
	creditor    DepositCreditor
	withdrawals WithdrawalStatusApplier
}

// WithChainDeposits 装配链上充值回调能力；secret 为空时端点返回 503。
func WithChainDeposits(apiKey, secret string, skew time.Duration, creditor DepositCreditor) Option {
	return func(server *Server) {
		server.chainWebhook = &chainWebhookConfig{apiKey: apiKey, secret: secret, skew: skew, creditor: creditor}
	}
}

func WithChainWithdrawalStatuses(applier WithdrawalStatusApplier) Option {
	return func(server *Server) {
		if server.chainWebhook == nil {
			server.chainWebhook = &chainWebhookConfig{}
		}
		server.chainWebhook.withdrawals = applier
	}
}

func WithWithdrawals(withdrawals WithdrawalService) Option {
	return func(server *Server) { server.withdrawals = withdrawals }
}

func WithDepositAddresses(addresses DepositAddressService) Option {
	return func(server *Server) { server.depositAddresses = addresses }
}
