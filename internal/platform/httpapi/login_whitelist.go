package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/operations"
	"net/http"
)

type LoginWhitelistService interface {
	ListLoginWhitelist(context.Context, string) (operations.LoginWhitelist, error)
	SaveLoginWhitelist(context.Context, string, operations.LoginWhitelistInput) (operations.LoginWhitelistEntry, error)
	DeleteLoginWhitelist(context.Context, string, string) error
}

func WithLoginWhitelist(v LoginWhitelistService) Option {
	return func(s *Server) { s.loginWhitelist = v }
}
func (s *Server) loginWhitelistHandler(w http.ResponseWriter, r *http.Request) {
	if s.loginWhitelist == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	var out any
	var e error
	switch r.Method {
	case "GET":
		out, e = s.loginWhitelist.ListLoginWhitelist(r.Context(), c.Subject)
	case "POST":
		var in operations.LoginWhitelistInput
		if !decodeSecurity(w, r, &in) {
			return
		}
		out, e = s.loginWhitelist.SaveLoginWhitelist(r.Context(), c.Subject, in)
	case "DELETE":
		e = s.loginWhitelist.DeleteLoginWhitelist(r.Context(), c.Subject, r.PathValue("entryID"))
	}
	if errors.Is(e, operations.ErrInvalidLoginWhitelist) {
		writeJSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	if e != nil {
		userControlError(w, e)
		return
	}
	if r.Method == "DELETE" {
		w.WriteHeader(204)
		return
	}
	writeJSON(w, 200, out)
}
