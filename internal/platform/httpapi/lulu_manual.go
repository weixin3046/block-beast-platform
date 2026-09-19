package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/externaldraw"
	"net/http"
)

type LuluManualService interface {
	ConfirmManual(context.Context, string, externaldraw.ManualInput) (externaldraw.ManualResult, error)
}

func WithLuluManualResults(service LuluManualService) Option {
	return func(s *Server) { s.luluManual = service }
}

func (s *Server) confirmLuluManual(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims.Subject == "" {
		writeJSON(w, 401, map[string]string{"error": "请先登录"})
		return
	}
	if s.luluManual == nil {
		writeJSON(w, 503, map[string]string{"error": "LULU 服务未配置"})
		return
	}
	var in externaldraw.ManualInput
	if !luluDecode(w, r, &in) {
		return
	}
	out, err := s.luluManual.ConfirmManual(r.Context(), claims.Subject, in)
	switch {
	case err == nil:
		writeJSON(w, 200, out)
	case errors.Is(err, externaldraw.ErrInvalidEvent):
		writeJSON(w, 400, map[string]string{"error": "补录参数无效，请检查游戏、期号、结果和原因"})
	case errors.Is(err, externaldraw.ErrManualNotFound):
		writeJSON(w, 404, map[string]string{"error": "待补录期号不存在"})
	case errors.Is(err, externaldraw.ErrManualConflict):
		writeJSON(w, 409, map[string]string{"error": "该期已有结果、已结束或尚未到开奖时间，不能补录"})
	default:
		writeJSON(w, 500, map[string]string{"error": "操作失败，请稍后重试"})
	}
}
