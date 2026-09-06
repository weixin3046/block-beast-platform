package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/block-beast/platform/internal/application/virtualbot"
)

type RobotPlanService interface {
	ListPlans(context.Context, string, int, int) (virtualbot.PlanPage, error)
	GetPlan(context.Context, string) (virtualbot.Plan, error)
	CreatePlan(context.Context, string, string, virtualbot.PlanInput) (virtualbot.Plan, error)
	UpdatePlan(context.Context, string, string, virtualbot.PlanInput) (virtualbot.Plan, error)
	SetPlanEnabled(context.Context, string, string, bool) (virtualbot.Plan, error)
	DeletePlan(context.Context, string, string) error
}

func WithRobotPlans(s RobotPlanService) Option { return func(server *Server) { server.robotPlans = s } }

func (s *Server) handleRobotPlans(w http.ResponseWriter, r *http.Request) {
	if s.robotPlans == nil {
		writeJSON(w, 503, map[string]string{"error": "机器人计划服务不可用"})
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	id := r.PathValue("planID")
	var result any
	var err error
	status := 200
	switch r.Method {
	case http.MethodGet:
		if id != "" {
			result, err = s.robotPlans.GetPlan(r.Context(), id)
		} else {
			limit, offset := 50, 0
			if v := r.URL.Query().Get("limit"); v != "" {
				limit, err = strconv.Atoi(v)
			}
			if v := r.URL.Query().Get("offset"); v != "" && err == nil {
				offset, err = strconv.Atoi(v)
			}
			if err != nil || limit < 1 || limit > 100 || offset < 0 {
				writeJSON(w, 400, map[string]string{"error": "分页参数无效"})
				return
			}
			result, err = s.robotPlans.ListPlans(r.Context(), r.URL.Query().Get("q"), limit, offset)
		}
	case http.MethodDelete:
		err = s.robotPlans.DeletePlan(r.Context(), claims.Subject, id)
		result = map[string]bool{"deleted": true}
	default:
		var raw map[string]json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&raw) != nil || raw == nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return
		}
		required := []string{"enabled"}
		toggle := strings.HasSuffix(r.URL.Path, "/enabled")
		if !toggle {
			required = append(required, "user_id", "game_type", "game_room_id", "currency", "min_stake_minor", "max_stake_minor", "selections", "skip_min", "skip_max")
		}
		if r.Method == http.MethodPost {
			required = append(required, "request_id")
		}
		for _, key := range required {
			v, ok := raw[key]
			if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return
			}
		}
		data, _ := json.Marshal(raw)
		if toggle {
			var in struct {
				Enabled bool `json:"enabled"`
			}
			if decodeStrictConfig(data, &in) != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return
			}
			result, err = s.robotPlans.SetPlanEnabled(r.Context(), claims.Subject, id, in.Enabled)
		} else if r.Method == http.MethodPost {
			var in struct {
				virtualbot.PlanInput
				RequestID string `json:"request_id"`
			}
			if decodeStrictConfig(data, &in) != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return
			}
			result, err = s.robotPlans.CreatePlan(r.Context(), claims.Subject, in.RequestID, in.PlanInput)
			status = 201
		} else {
			var in virtualbot.PlanInput
			if decodeStrictConfig(data, &in) != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return
			}
			result, err = s.robotPlans.UpdatePlan(r.Context(), claims.Subject, id, in)
		}
	}
	if err != nil {
		message := "机器人计划操作失败"
		status = 500
		switch {
		case errors.Is(err, virtualbot.ErrInvalidPlan):
			status = 400
			message = err.Error()
		case errors.Is(err, virtualbot.ErrPlanNotFound):
			status = 404
			message = err.Error()
		case errors.Is(err, virtualbot.ErrPlanConflict):
			status = 409
			message = err.Error()
		case errors.Is(err, virtualbot.ErrForbidden):
			status = 403
			message = err.Error()
		}
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	writeJSON(w, status, result)
}
