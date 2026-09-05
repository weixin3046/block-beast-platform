package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/block-beast/platform/internal/application/adminsecurity"
)

type AdminSecurityService interface {
	Status(context.Context, string) (adminsecurity.Status, error)
	Set(context.Context, string, string, string, string) error
	Verify(context.Context, string, string, string) error
}

func WithAdminSecurity(service AdminSecurityService) Option {
	return func(s *Server) { s.adminSecurity = service }
}
func securityError(w http.ResponseWriter, err error) {
	status := 500
	switch {
	case errors.Is(err, adminsecurity.ErrInvalid):
		status = 400
	case errors.Is(err, adminsecurity.ErrForbidden):
		status = 403
	case errors.Is(err, adminsecurity.ErrNotSet):
		status = 409
	case errors.Is(err, adminsecurity.ErrIncorrect), errors.Is(err, adminsecurity.ErrLoginPassword):
		status = 401
	case errors.Is(err, adminsecurity.ErrLocked):
		status = 429
		w.Header().Set("Retry-After", "900")
	}
	message := "后台操作密码服务异常"
	if status != 500 {
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func (s *Server) verifyAdminSecurity(w http.ResponseWriter, r *http.Request, level, password string) bool {
	if password == "" {
		securityError(w, adminsecurity.ErrInvalid)
		return false
	}
	if s.adminSecurity == nil {
		writeJSON(w, 503, map[string]string{"error": "后台操作密码服务不可用"})
		return false
	}
	claims, _ := ClaimsFromContext(r.Context())
	if err := s.adminSecurity.Verify(r.Context(), claims.Subject, level, password); err != nil {
		securityError(w, err)
		return false
	}
	return true
}
func (s *Server) adminSecurityStatus(w http.ResponseWriter, r *http.Request) {
	if s.adminSecurity == nil {
		writeJSON(w, 503, map[string]string{"error": "后台操作密码服务不可用"})
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	status, err := s.adminSecurity.Status(r.Context(), claims.Subject)
	if err != nil {
		securityError(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func decodeSecurity(w http.ResponseWriter, r *http.Request, target any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		securityError(w, adminsecurity.ErrInvalid)
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		securityError(w, adminsecurity.ErrInvalid)
		return false
	}
	return true
}
func (s *Server) setAdminSecurity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password      string `json:"password"`
		LoginPassword string `json:"login_password"`
	}
	if !decodeSecurity(w, r, &body) {
		return
	}
	if s.adminSecurity == nil {
		writeJSON(w, 503, map[string]string{"error": "后台操作密码服务不可用"})
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	if err := s.adminSecurity.Set(r.Context(), claims.Subject, r.PathValue("level"), body.LoginPassword, body.Password); err != nil {
		securityError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) verifyAdminSecurityEndpoint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeSecurity(w, r, &body) {
		return
	}
	if s.verifyAdminSecurity(w, r, r.PathValue("level"), body.Password) {
		w.WriteHeader(204)
	}
}

// 验证后移除密码，禁止进入业务配置及配置审计。
func (s *Server) secondPassword(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if !decodeSecurity(w, r, &body) {
			return
		}
		var password string
		if err := json.Unmarshal(body["second_password"], &password); err != nil {
			writeJSON(w, 400, map[string]string{"error": "请在请求体最外层提交字符串 second_password（二级操作密码）"})
			return
		}
		if password == "" {
			writeJSON(w, 400, map[string]string{"error": "请填写后台全局二级操作密码 second_password"})
			return
		}
		if !s.verifyAdminSecurity(w, r, "second", password) {
			return
		}
		delete(body, "second_password")
		data, err := json.Marshal(body)
		if err != nil {
			securityError(w, err)
			return
		}
		request := r.Clone(r.Context())
		request.Body = io.NopCloser(bytes.NewReader(data))
		request.ContentLength = int64(len(data))
		next(w, request)
	}
}
