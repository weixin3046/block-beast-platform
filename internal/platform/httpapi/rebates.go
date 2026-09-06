package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/rebate"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RebateService interface {
	ListConfigs(context.Context, rebate.ConfigQuery) ([]rebate.Config, error)
	UpdateConfig(context.Context, string, string, rebate.ConfigUpdate) (rebate.Config, error)
}

func (s *Server) rebateRecords(w http.ResponseWriter, r *http.Request) {
	service, ok := s.rebates.(interface {
		ListRecords(context.Context, rebate.RecordQuery) (rebate.Records, error)
	})
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "返水服务暂不可用"})
		return
	}
	values := r.URL.Query()
	q := rebate.RecordQuery{Currency: values.Get("currency"), Status: values.Get("status"), GameType: values.Get("game_type"), RoomID: values.Get("game_room_id"), Limit: queryLimit(r, 50), Offset: queryOffset(r)}
	for key, target := range map[string]*int64{"source_user_id": &q.SourceUserID, "beneficiary_user_id": &q.BeneficiaryUserID} {
		if v := values.Get(key); v != "" {
			n, e := strconv.ParseInt(v, 10, 64)
			if e != nil || n <= 0 {
				writeJSON(w, 400, map[string]string{"error": "用户ID无效"})
				return
			}
			*target = n
		}
	}
	for key, target := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
		if v := values.Get(key); v != "" {
			at, e := time.Parse(time.RFC3339, v)
			if e != nil {
				writeJSON(w, 400, map[string]string{"error": "时间参数无效"})
				return
			}
			*target = &at
		}
	}
	if strings.HasPrefix(r.URL.Path, "/v1/agents/me/") {
		c, ok := ClaimsFromContext(r.Context())
		if !ok || c.Subject == "" {
			writeJSON(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		q.ViewerID = c.Subject
	}
	out, e := service.ListRecords(r.Context(), q)
	if errors.Is(e, rebate.ErrInvalid) {
		writeJSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "查询返水明细失败"})
		return
	}
	writeJSON(w, 200, out)
}

func WithRebates(service RebateService) Option { return func(s *Server) { s.rebates = service } }
func (s *Server) rebateConfigs(w http.ResponseWriter, r *http.Request) {
	if s.rebates == nil {
		writeJSON(w, 503, map[string]string{"error": "返水服务暂不可用"})
		return
	}
	var result any
	var err error
	if r.Method == "GET" {
		q := r.URL.Query()
		result, err = s.rebates.ListConfigs(r.Context(), rebate.ConfigQuery{GameType: q.Get("game_type"), RoomID: q.Get("game_room_id"), Currency: q.Get("currency")})
	} else {
		var in struct {
			Version *int64         `json:"version"`
			Enabled *bool          `json:"enabled"`
			Levels  []rebate.Level `json:"levels"`
		}
		if !decodeSecurity(w, r, &in) {
			return
		}
		if in.Version == nil || in.Enabled == nil {
			writeJSON(w, 400, map[string]string{"error": "必须提供version、enabled和完整levels"})
			return
		}
		claims, _ := ClaimsFromContext(r.Context())
		result, err = s.rebates.UpdateConfig(r.Context(), claims.Subject, r.PathValue("configID"), rebate.ConfigUpdate{Version: *in.Version, Enabled: *in.Enabled, Levels: in.Levels})
	}
	status := 200
	switch {
	case errors.Is(err, rebate.ErrInvalid):
		status = 400
	case errors.Is(err, rebate.ErrForbidden):
		status = 403
	case errors.Is(err, rebate.ErrNotFound):
		status = 404
	case errors.Is(err, rebate.ErrConflict):
		status = 409
	case err != nil:
		writeJSON(w, 500, map[string]string{"error": "返水配置操作失败"})
		return
	}
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, status, result)
}
