package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/operations"
)

type UserAdminService interface {
	ListUsers(ctx context.Context, status, query string, limit int) ([]operations.User, error)
	SetUserStatus(ctx context.Context, userID, status string) error
}

func WithUserAdmin(service UserAdminService) Option {
	return func(server *Server) { server.userAdmin = service }
}

func (server *Server) adminUsers(writer http.ResponseWriter, request *http.Request) {
	items, err := server.userAdmin.ListUsers(request.Context(), request.URL.Query().Get("status"), request.URL.Query().Get("q"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list users"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) setUserStatus(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	err := server.userAdmin.SetUserStatus(request.Context(), request.PathValue("userID"), input.Status)
	switch {
	case errors.Is(err, operations.ErrInvalidUserStatus):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrUserNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update user status"})
	default:
		claims, _ := ClaimsFromContext(request.Context())
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "user.status.update", TargetType: "user", TargetID: request.PathValue("userID"), Payload: map[string]any{"status": input.Status}})
		writeJSON(writer, http.StatusOK, map[string]string{"status": input.Status})
	}
}
