package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/block-beast/platform/internal/application/credit"
)

func (s *Server) verifyFundsPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	return s.verifyAdminSecurity(w, r, "first", password)
}

func writeAdjustmentError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status, message := 500, "资金操作失败"
	switch {
	case errors.Is(err, credit.ErrInvalidAdjustment):
		status, message = 400, err.Error()
	case errors.Is(err, credit.ErrInvalidAmount):
		status, message = 400, "金额必须为正数且符合币种精度和余额范围"
	case errors.Is(err, credit.ErrInvalidCurrency):
		status, message = 400, "币种不存在或未启用"
	case errors.Is(err, credit.ErrAdjustmentForbidden):
		status, message = 403, err.Error()
	case errors.Is(err, credit.ErrVirtualAccountWithdrawal):
		status, message = 403, "虚拟账户不能下分或人工扣分"
	case errors.Is(err, credit.ErrAdjustmentConflict):
		status, message = 409, err.Error()
	case errors.Is(err, credit.ErrInsufficientBalance):
		status, message = 409, "可用余额不足"
	case errors.Is(err, credit.ErrUserNotFound):
		status, message = 404, "用户不存在"
	}
	writeJSON(w, status, map[string]string{"error": message})
	return true
}

func (s *Server) adminWalletAdjustment(w http.ResponseWriter, r *http.Request) {
	if s.credits == nil {
		writeJSON(w, 503, map[string]string{"error": "资金服务不可用"})
		return
	}
	var body struct {
		credit.AdjustmentInput
		FirstPassword string `json:"first_password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求参数无效"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请求参数无效"})
		return
	}
	if !s.verifyFundsPassword(w, r, body.FirstPassword) {
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	body.OperatorID = claims.Subject
	id, err := s.resolvePublicUserID(r.Context(), body.UserID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "用户不存在"})
		return
	}
	body.UserID = id
	result, err := s.credits.AdjustWallet(r.Context(), body.AdjustmentInput)
	if writeAdjustmentError(w, err) {
		return
	}
	s.writePublicJSON(w, r, 200, result)
}
