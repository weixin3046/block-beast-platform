package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	chainapp "github.com/block-beast/platform/internal/application/chain"
	chaindomain "github.com/block-beast/platform/internal/domain/chain"
)

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
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Api-Key")), []byte(server.chainWebhook.apiKey)) != 1 {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "invalid webhook api key"})
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
	var input chainapp.PQPADepositWebhook
	if err := json.Unmarshal(rawBody, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	result, processed, err := server.chainWebhook.creditor.CreditPQPADeposit(request.Context(), input)
	switch {
	case errors.Is(err, chainapp.ErrUnknownDepositAddress), errors.Is(err, chainapp.ErrUnsupportedAsset):
		writeJSON(writer, http.StatusOK, map[string]int{"code": 0})
		return
	case errors.Is(err, chainapp.ErrMissingFields), errors.Is(err, chainapp.ErrInvalidAmount):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to credit deposit"})
		return
	}
	if !processed {
		writeJSON(writer, http.StatusOK, map[string]int{"code": 0})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"code": 0, "deposit": result})
}

func (server *Server) chainWithdrawalWebhook(writer http.ResponseWriter, request *http.Request) {
	if server.chainWebhook == nil || server.chainWebhook.secret == "" || server.chainWebhook.withdrawals == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chain withdrawal webhook is unavailable"})
		return
	}
	rawBody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unable to read request body"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Api-Key")), []byte(server.chainWebhook.apiKey)) != 1 {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "invalid webhook api key"})
		return
	}
	err = chaindomain.VerifyWebhook(server.chainWebhook.secret, request.Method, request.URL.Path, request.Header.Get("X-Timestamp"), request.Header.Get("X-Nonce"), rawBody, request.Header.Get("X-Signature"), time.Now().UTC(), server.chainWebhook.skew)
	if errors.Is(err, chaindomain.ErrTimestampOutOfRange) || errors.Is(err, chaindomain.ErrInvalidSignature) {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	var input chainapp.PQPAWithdrawalWebhook
	if err := json.Unmarshal(rawBody, &input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	_, err = server.chainWebhook.withdrawals.ApplyPQPAWithdrawal(request.Context(), input)
	if err != nil {
		if errors.Is(err, chainapp.ErrWithdrawalNotFound) {
			writeJSON(writer, http.StatusOK, map[string]int{"code": 0})
			return
		}
		if errors.Is(err, chainapp.ErrMissingFields) || errors.Is(err, chainapp.ErrWithdrawalState) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to apply withdrawal status"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]int{"code": 0})
}
