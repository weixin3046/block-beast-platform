package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	agentapp "github.com/block-beast/platform/internal/application/agent"
)

type AgentService interface {
	Bind(ctx context.Context, userID, parentID string) error
}

func WithAgents(service AgentService) Option { return func(server *Server) { server.agents = service } }

func (server *Server) bindAgent(writer http.ResponseWriter, request *http.Request) {
	if server.agents == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "agent service is unavailable"})
		return
	}
	var input struct {
		ParentUserID string `json:"parent_user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok || claims.Subject == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	err := server.agents.Bind(request.Context(), claims.Subject, input.ParentUserID)
	switch {
	case errors.Is(err, agentapp.ErrInvalidRelation):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, agentapp.ErrRelationExists):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to bind agent"})
	default:
		writeJSON(writer, http.StatusCreated, map[string]string{"status": "bound"})
	}
}
