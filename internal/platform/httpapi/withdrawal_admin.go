package httpapi

import (
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	chainapp "github.com/block-beast/platform/internal/application/chain"
)

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
	server.writePublicJSON(writer, request, http.StatusOK, withdrawal)
}

func (server *Server) approveWithdrawal(writer http.ResponseWriter, request *http.Request) {
	if server.withdrawals == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "withdrawals are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	withdrawal, err := server.withdrawals.ApproveWithdrawal(request.Context(), request.PathValue("withdrawalID"), claims.Subject)
	switch {
	case errors.Is(err, chainapp.ErrWithdrawalNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, chainapp.ErrWithdrawalState):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, chainapp.ErrMissingFields):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to approve withdrawal"})
	default:
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "withdrawal.approve", TargetType: "withdrawal", TargetID: withdrawal.WithdrawalID, Payload: map[string]any{"status": withdrawal.Status}})
		server.writePublicJSON(writer, request, http.StatusOK, withdrawal)
	}
}
