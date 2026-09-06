package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/task"
	"net/http"
	"strconv"
)

type taskManager interface {
	ChangeTaskState(context.Context, string, string, *bool) error
	ListProgress(context.Context, task.ProgressQuery) (task.ProgressPage, error)
}

func (s *Server) taskState(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.tasks.(taskManager)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "task service is unavailable"})
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
	e := svc.ChangeTaskState(r.Context(), c.Subject, r.PathValue("taskID"), enabled)
	switch {
	case errors.Is(e, task.ErrTaskConfigNotFound):
		writeJSON(w, 404, map[string]string{"error": "task config not found"})
	case errors.Is(e, task.ErrTaskForbidden):
		writeJSON(w, 403, map[string]string{"error": "无权管理任务"})
	case e != nil:
		writeJSON(w, 500, map[string]string{"error": "任务配置操作失败"})
	default:
		writeJSON(w, 200, map[string]bool{"success": true})
	}
}
func (s *Server) taskProgress(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.tasks.(taskManager)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "task service is unavailable"})
		return
	}
	q := task.ProgressQuery{TaskID: r.URL.Query().Get("task_id"), Date: r.URL.Query().Get("date"), Limit: 50}
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
			e = task.ErrInvalidTask
		}
	}
	if e != nil || q.Limit < 1 || q.Limit > 100 || q.Offset < 0 {
		writeJSON(w, 400, map[string]string{"error": "分页参数无效"})
		return
	}
	out, e := svc.ListProgress(r.Context(), q)
	if errors.Is(e, task.ErrInvalidTask) {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	if e != nil {
		writeJSON(w, 500, map[string]string{"error": "任务进度查询失败"})
		return
	}
	writeJSON(w, 200, out)
}
