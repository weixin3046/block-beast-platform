package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	chainapp "github.com/block-beast/platform/internal/application/chain"
)

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
		if userID != "" {
			internalID, err := server.resolvePublicUserID(request.Context(), userID)
			if err != nil {
				writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			userID = internalID
		}
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
	server.writePublicJSON(writer, request, http.StatusOK, address)
}

func (server *Server) createDepositAddress(writer http.ResponseWriter, request *http.Request) {
	if server.depositAddresses == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "deposit addresses are unavailable"})
		return
	}
	var input struct {
		AccountID string `json:"account_id"`
		ChainCode string `json:"chain_code"`
		TokenCode string `json:"token_code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	userID := input.AccountID
	if claims, ok := ClaimsFromContext(request.Context()); ok {
		userID = claims.Subject
	} else if userID != "" {
		internalID, err := server.resolvePublicUserID(request.Context(), userID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		userID = internalID
	}
	address, err := server.depositAddresses.CreateDepositAddress(request.Context(), userID, input.ChainCode, input.TokenCode)
	if errors.Is(err, chainapp.ErrMissingFields) || errors.Is(err, chainapp.ErrUnsupportedAsset) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "unable to create deposit address"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusCreated, address)
}
