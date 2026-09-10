package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/application/lulu"
	"github.com/block-beast/platform/internal/domain/wallet"
)

type LuluService interface {
	AccountBalance(context.Context, string) (lulu.AccountBalance, error)
	Transfers(context.Context, string, string, int, int) (lulu.TransferPage, error)
	SendLoginCode(context.Context, string, string, int64) error
	PhoneLogin(context.Context, string, string, string, int64) (lulu.Config, error)
	Config(context.Context) (lulu.Config, error)
	UpdateConfig(context.Context, string, lulu.ConfigUpdate) (lulu.Config, error)
	Create(context.Context, string, string, lulu.Input) (lulu.Order, error)
	List(context.Context, string, string, string, string, int, int) ([]lulu.Order, error)
	Review(context.Context, string, string, string, string) (lulu.Order, error)
	Health(context.Context, string) (lulu.Health, error)
}

func WithLulu(service LuluService) Option { return func(s *Server) { s.lulu = service } }
func (s *Server) luluActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	// This payment channel always requires a real session, even in local development.
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims.Subject == "" {
		writeJSON(w, 401, map[string]string{"error": "请先登录"})
		return "", false
	}
	if s.lulu == nil {
		writeJSON(w, 503, map[string]string{"error": "LULU 服务未配置"})
		return "", false
	}
	return claims.Subject, true
}
func luluError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var pending *lulu.PendingDepositError
	if errors.As(err, &pending) {
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("噜噜账号 %s 有一笔待完成的充值订单，请先完成上一笔；如未转赠，请等待 %d 秒后重新提交。", pending.LuluUID, pending.RetryAfterSeconds), "code": "lulu_deposit_pending", "lulu_uid": pending.LuluUID, "retry_after_seconds": pending.RetryAfterSeconds})
		return true
	}
	status, msg := 500, "LULU 操作失败"
	switch {
	case errors.Is(err, lulu.ErrBalanceTimeout):
		status, msg = 504, "噜噜余额查询超时，请稍后重试"
	case errors.Is(err, lulu.ErrBalanceUnavailable):
		status, msg = 502, "噜噜余额查询失败，请稍后重试"
	case errors.Is(err, lulu.ErrTokenInvalid):
		status, msg = 502, "噜噜登录已失效，请重新获取短信验证码登录"
	case errors.Is(err, lulu.ErrLoginLimited):
		status, msg = 429, "噜噜登录操作过于频繁，请稍后重试"
	case errors.Is(err, lulu.ErrLoginFailed):
		status, msg = 502, "噜噜短信或登录失败，请核对验证码和上游配置"
	case errors.Is(err, lulu.ErrEncryption):
		status, msg = 503, "LULU 凭据加密配置不可用"
	case errors.Is(err, lulu.ErrConfigInvalid):
		status, msg = 400, "LULU 配置无效"
	case errors.Is(err, lulu.ErrConfigConflict):
		status, msg = 409, "配置版本已过期，或需先关闭通道并处理在途订单"
	case errors.Is(err, lulu.ErrSameAccount):
		status, msg = 400, "玩家噜噜账号不能与平台收付账号相同，请填写玩家自己的噜噜账号 ID"
	case errors.Is(err, lulu.ErrInvalid):
		status, msg = 400, "参数无效，数量必须是正整数字符串"
	case errors.Is(err, lulu.ErrForbidden):
		status, msg = 403, "无权执行该操作"
	case errors.Is(err, lulu.ErrNotFound):
		status, msg = 404, "订单不存在"
	case errors.Is(err, lulu.ErrConflict):
		status, msg = 409, "订单状态或请求参数冲突"
	case errors.Is(err, wallet.ErrInsufficientFunds):
		status, msg = 409, "可用余额不足或冻结状态不符"
	case errors.Is(err, lulu.ErrDisabled), errors.Is(err, wallet.ErrCurrencyDisabled), errors.Is(err, wallet.ErrUnknownCurrency):
		status, msg = 503, "LULU 通道未启用"
	}
	writeJSON(w, status, map[string]string{"error": msg})
	return true
}
func luluDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "请求参数无效"})
		return false
	}
	return true
}
func (s *Server) luluConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.luluActor(w, r); !ok {
		return
	}
	cfg, err := s.lulu.Config(r.Context())
	if luluError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": cfg.Enabled, "receiver_uid": cfg.ReceiverUID, "currency": lulu.Currency, "decimals": lulu.Decimals, "exchange_ratio": "1:1", "deposit_verification": "transfer_record", "withdrawal_review": "manual"})
}
func (s *Server) createLuluOrder(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.luluActor(w, r)
		if !ok {
			return
		}
		var in lulu.Input
		if !luluDecode(w, r, &in) {
			return
		}
		out, err := s.lulu.Create(r.Context(), actor, kind, in)
		if !luluError(w, err) {
			s.writePublicJSON(w, r, 201, out)
		}
	}
}
func (s *Server) listLuluOrders(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.luluActor(w, r)
		if !ok {
			return
		}
		user, reviewer := actor, ""
		if admin {
			user, reviewer = "", actor
		}
		limit, offset := parsePagination(r)
		out, err := s.lulu.List(r.Context(), user, reviewer, r.URL.Query().Get("kind"), r.URL.Query().Get("status"), limit, offset)
		if !luluError(w, err) {
			s.writePublicJSON(w, r, 200, map[string]any{"items": out})
		}
	}
}
func (s *Server) reviewLuluOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.luluActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Action        string `json:"action"`
		Evidence      string `json:"evidence"`
		FirstPassword string `json:"first_password"`
	}
	if !luluDecode(w, r, &in) || !s.verifyFundsPassword(w, r, in.FirstPassword) {
		return
	}
	out, err := s.lulu.Review(r.Context(), r.PathValue("orderID"), actor, in.Action, in.Evidence)
	if !luluError(w, err) {
		s.writePublicJSON(w, r, 200, out)
	}
}
func (s *Server) luluHealth(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.luluActor(w, r)
	if !ok {
		return
	}
	out, err := s.lulu.Health(r.Context(), actor)
	if !luluError(w, err) {
		writeJSON(w, 200, out)
	}
}

func (s *Server) adminLuluConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.luluActor(w, r); !ok {
		return
	}
	out, err := s.lulu.Config(r.Context())
	if !luluError(w, err) {
		writeJSON(w, 200, out)
	}
}
func (s *Server) updateLuluConfig(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.luluActor(w, r)
	if !ok {
		return
	}
	var in struct {
		APIURL      *string    `json:"api_url"`
		ScanStartAt *time.Time `json:"scan_start_at"`
		ProtocolKey string     `json:"protocol_key"`
		Enabled     *bool      `json:"enabled"`
		Version     int64      `json:"version"`
	}
	if !luluDecode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		luluError(w, lulu.ErrConfigInvalid)
		return
	}
	cfg, err := s.lulu.Config(r.Context())
	if luluError(w, err) {
		return
	}
	out, err := s.lulu.UpdateConfig(r.Context(), actor, lulu.ConfigUpdate{ReceiverUID: cfg.ReceiverUID, Enabled: *in.Enabled, Version: in.Version, APIURL: in.APIURL, ScanStartAt: in.ScanStartAt, ProtocolKey: in.ProtocolKey})
	if !luluError(w, err) {
		writeJSON(w, 200, out)
	}
}

func (s *Server) luluSMSLogin(send bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.luluActor(w, r)
		if !ok {
			return
		}
		if send {
			var in struct {
				Phone   string `json:"phone"`
				Version int64  `json:"version"`
			}
			if !luluDecode(w, r, &in) {
				return
			}
			err := s.lulu.SendLoginCode(r.Context(), actor, in.Phone, in.Version)
			if !luluError(w, err) {
				writeJSON(w, 200, map[string]string{"status": "sent"})
			}
			return
		}
		var in struct {
			Phone   string `json:"phone"`
			Code    string `json:"code"`
			Version int64  `json:"version"`
		}
		if !luluDecode(w, r, &in) {
			return
		}
		out, err := s.lulu.PhoneLogin(r.Context(), actor, in.Phone, in.Code, in.Version)
		if !luluError(w, err) {
			writeJSON(w, 200, out)
		}
	}
}

func (s *Server) luluTransfers(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.luluActor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	direction := q.Get("direction")
	if direction == "" {
		direction = "received"
	}
	page, size := 1, 50
	var e error
	if q.Has("page") {
		page, e = strconv.Atoi(q.Get("page"))
		if e != nil {
			luluError(w, lulu.ErrInvalid)
			return
		}
	}
	if q.Has("size") {
		size, e = strconv.Atoi(q.Get("size"))
		if e != nil {
			luluError(w, lulu.ErrInvalid)
			return
		}
	}
	out, e := s.lulu.Transfers(r.Context(), actor, direction, page, size)
	if !luluError(w, e) {
		writeJSON(w, 200, out)
	}
}

func (s *Server) luluBalance(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.luluActor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out, err := s.lulu.AccountBalance(r.Context(), actor)
	if !luluError(w, err) {
		writeJSON(w, 200, out)
	}
}
