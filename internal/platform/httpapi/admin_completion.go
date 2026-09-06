package httpapi

import (
	"context"
	"errors"
	agentapp "github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/domain/identity"
	"net/http"
	"strconv"
)

type AdminBetVoidService interface {
	VoidBet(context.Context, operations.BetVoidInput) (operations.BetVoidResult, error)
	ListBetVoids(context.Context, string, int, int) (operations.BetVoidPage, error)
}
type AdminPlayerCreator interface {
	CreatePlayerAccount(context.Context, operations.PlayerAccountInput) (operations.PlayerAccount, error)
}
type AdminRelationBinder interface {
	AdminBind(context.Context, string, int64, int64) (agentapp.AdminRelation, error)
}

func WithAdminCompletion(v AdminBetVoidService, p AdminPlayerCreator, b AdminRelationBinder) Option {
	return func(s *Server) { s.adminBetVoids = v; s.adminPlayerCreator = p; s.adminRelationBinder = b }
}
func (s *Server) registerAdminCompletionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/admin/bets/{betID}/void", s.protectRoles(s.secondPassword(s.adminVoidBet), identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/bet-voids", s.protectRoles(s.adminListBetVoids, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/users", s.protectRoles(s.secondPassword(s.adminCreatePlayer), identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/users/{userID}/agent-relation", s.protectRoles(s.secondPassword(s.adminBindRelation), identity.RoleAdmin, identity.RoleOperator))
}
func completionError(w http.ResponseWriter, e error) {
	code := 500
	switch {
	case errors.Is(e, operations.ErrInvalidBetVoid), errors.Is(e, operations.ErrInvalidPlayerAccount), errors.Is(e, operations.ErrInvalidAvatar), errors.Is(e, agentapp.ErrInvalidRelation), errors.Is(e, agentapp.ErrVirtualRelation):
		code = 400
	case errors.Is(e, operations.ErrAdminCompletionForbidden), errors.Is(e, agentapp.ErrAdminBindForbidden):
		code = 403
	case errors.Is(e, operations.ErrBetVoidNotFound), errors.Is(e, agentapp.ErrRelationUserNotFound):
		code = 404
	case errors.Is(e, operations.ErrBetVoidConflict), errors.Is(e, operations.ErrBetNotVoidable), errors.Is(e, operations.ErrBetVoidBalanceOverflow), errors.Is(e, identity.ErrLoginNameTaken), errors.Is(e, agentapp.ErrRelationExists):
		code = 409
	}
	if code == 500 {
		writeJSON(w, code, map[string]string{"error": "用户管理操作失败"})
		return
	}
	writeJSON(w, code, map[string]string{"error": e.Error()})
}
func (s *Server) adminVoidBet(w http.ResponseWriter, r *http.Request) {
	if s.adminBetVoids == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	var in operations.BetVoidInput
	if !decodeSecurity(w, r, &in) {
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	in.OperatorID = c.Subject
	in.BetID = r.PathValue("betID")
	out, e := s.adminBetVoids.VoidBet(r.Context(), in)
	if e != nil {
		completionError(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) adminListBetVoids(w http.ResponseWriter, r *http.Request) {
	if s.adminBetVoids == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	out, e := s.adminBetVoids.ListBetVoids(r.Context(), c.Subject, queryLimit(r, 50), queryOffset(r))
	if e != nil {
		completionError(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) adminCreatePlayer(w http.ResponseWriter, r *http.Request) {
	if s.adminPlayerCreator == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	var in operations.PlayerAccountInput
	if !decodeSecurity(w, r, &in) {
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	in.ActorUserID = c.Subject
	out, e := s.adminPlayerCreator.CreatePlayerAccount(r.Context(), in)
	if e != nil {
		completionError(w, e)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) adminBindRelation(w http.ResponseWriter, r *http.Request) {
	if s.adminRelationBinder == nil {
		writeJSON(w, 503, map[string]string{"error": "用户管理服务不可用"})
		return
	}
	var in struct {
		ParentUserID int64 `json:"parent_user_id"`
	}
	if !decodeSecurity(w, r, &in) {
		return
	}
	id, e := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if e != nil {
		completionError(w, agentapp.ErrInvalidRelation)
		return
	}
	c, _ := ClaimsFromContext(r.Context())
	out, e := s.adminRelationBinder.AdminBind(r.Context(), c.Subject, id, in.ParentUserID)
	if e != nil {
		completionError(w, e)
		return
	}
	writeJSON(w, 200, out)
}
