package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/operations"
	"net/http"
	"strconv"
	"strings"
)

type UserControlService interface {
	ConvertUserToVirtual(context.Context, string, int64) error
	ResetUserPassword(context.Context, string, int64, string, string) error
	SetUserMuted(context.Context, string, int64, bool) error
	SearchUsers(context.Context, operations.UserSearch) ([]operations.User, error)
}

func WithUserControls(s UserControlService) Option {
	return func(server *Server) { server.userControls = s }
}
func userControlError(w http.ResponseWriter, err error) {
	code := 500
	message := "用户管理操作失败"
	switch {
	case errors.Is(err, operations.ErrUserNotFound):
		code = 404
	case errors.Is(err, operations.ErrUserControlInvalid), errors.Is(err, operations.ErrResetPasswordEmpty):
		code = 400
	case errors.Is(err, operations.ErrUserControlForbidden):
		code = 403
	}
	if code != 500 {
		message = err.Error()
	}
	writeJSON(w, code, map[string]string{"error": message})
}
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"new_password"`
	}
	if err := decodeStrictJSON(w, r, &in); err != nil {
		return
	}
	if s.userControls == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	id, e := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if e != nil {
		userControlError(w, operations.ErrUserNotFound)
		return
	}
	kind := "login"
	if strings.HasSuffix(r.URL.Path, "/secondary-password") {
		kind = "secondary"
	}
	c, _ := ClaimsFromContext(r.Context())
	if e = s.userControls.ResetUserPassword(r.Context(), c.Subject, id, kind, in.Password); e != nil {
		userControlError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) setUserMuted(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Muted *bool `json:"muted"`
	}
	if err := decodeStrictJSON(w, r, &in); err != nil {
		return
	}
	if in.Muted == nil {
		userControlError(w, operations.ErrUserControlInvalid)
		return
	}
	if s.userControls == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	id, e := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if e != nil {
		userControlError(w, operations.ErrUserNotFound)
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	if e = s.userControls.SetUserMuted(r.Context(), c.Subject, id, *in.Muted); e != nil {
		userControlError(w, e)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) convertUserToVirtual(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decodeSecurity(w, r, &in) {
		return
	}
	if s.userControls == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || id < 10001 || id > 99999 {
		userControlError(w, operations.ErrUserNotFound)
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	if err = s.userControls.ConvertUserToVirtual(r.Context(), claims.Subject, id); err != nil {
		userControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": id, "is_virtual": true})
}
