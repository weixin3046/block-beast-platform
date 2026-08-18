package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	chainapp "github.com/block-beast/platform/internal/application/chain"
	"github.com/block-beast/platform/internal/domain/wallet"
)

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
		DestinationMemo    string `json:"destination_memo"`
		ChainCode          string `json:"chain_code"`
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
	} else if accountID != "" {
		internalID, err := server.resolvePublicUserID(request.Context(), accountID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		accountID = internalID
	}
	if accountID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account is required"})
		return
	}
	withdrawal, err := server.withdrawals.RequestWithdrawal(request.Context(), chainapp.WithdrawalInput{
		UserID:             accountID,
		ClientRequestID:    input.ClientRequestID,
		DestinationAddress: input.DestinationAddress,
		DestinationMemo:    input.DestinationMemo,
		ChainCode:          input.ChainCode,
		Currency:           input.Currency,
		AmountMinor:        input.AmountMinor,
	})
	switch {
	case errors.Is(err, chainapp.ErrMissingFields),
		errors.Is(err, chainapp.ErrInvalidAmount),
		errors.Is(err, chainapp.ErrUnsupportedAsset),
		errors.Is(err, chainapp.ErrInvalidWithdrawalAddress),
		errors.Is(err, chainapp.ErrWithdrawalBelowMinimum),
		errors.Is(err, chainapp.ErrWithdrawalAboveMaximum):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, chainapp.ErrWithdrawalDailyLimit):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
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
	server.writePublicJSON(writer, request, http.StatusCreated, withdrawal)
}
