package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/credit"
	"net/http"
	"strconv"
	"strings"
)

type spinManager interface {
	ChangeSpinState(context.Context, string, string, *bool) error
	ListSpinRecords(context.Context, credit.SpinRecordQuery) (credit.SpinRecordPage, error)
}

func (s *Server) spinState(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.credits.(spinManager)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var enabled *bool
	if r.Method == "PUT" {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeSecurity(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return
		}
		enabled = in.Enabled
	}
	c, _ := ClaimsFromContext(r.Context())
	e := svc.ChangeSpinState(r.Context(), c.Subject, r.PathValue("spinID"), enabled)
	switch {
	case errors.Is(e, credit.ErrSpinConfigNotFound):
		writeJSON(w, 404, map[string]string{"error": "activity is unavailable"})
	case errors.Is(e, credit.ErrInvalidSpinConfig):
		writeJSON(w, 400, map[string]string{"error": "invalid spin configuration"})
	case errors.Is(e, credit.ErrSpinManagementForbidden):
		writeJSON(w, 403, map[string]string{"error": "无权管理转盘"})
	case e != nil:
		writeJSON(w, 500, map[string]string{"error": "转盘配置操作失败"})
	default:
		writeJSON(w, 200, map[string]bool{"success": true})
	}
}
func (s *Server) spinRecords(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.credits.(spinManager)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "credit service is unavailable"})
		return
	}
	q := credit.SpinRecordQuery{SpinID: r.URL.Query().Get("spin_id"), Currency: strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency"))), Limit: 50}
	var e error
	if v := r.URL.Query().Get("limit"); v != "" {
		q.Limit, e = strconv.Atoi(v)
	}
	if v := r.URL.Query().Get("offset"); v != "" && e == nil {
		q.Offset, e = strconv.Atoi(v)
	}
	if v := r.URL.Query().Get("user_id"); v != "" && e == nil {
		q.UserID, e = strconv.ParseInt(v, 10, 64)
		if q.UserID <= 0 {
			e = credit.ErrInvalidSpinConfig
		}
	}
	if e != nil || q.Limit < 1 || q.Limit > 100 || q.Offset < 0 {
		writeJSON(w, 400, map[string]string{"error": "分页参数无效"})
		return
	}
	result, e := svc.ListSpinRecords(r.Context(), q)
	if errors.Is(e, credit.ErrInvalidSpinConfig) {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "抽奖记录查询失败"})
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) retiredActivityBatch(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 410, map[string]string{"error": "整批配置接口已停用，请使用单条配置接口"})
}
