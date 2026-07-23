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
	GetRelation(ctx context.Context, userID string) (agentapp.Relation, error)
	SetCommissionRate(ctx context.Context, agentID string, rateBasisPoints int, operatorID string) error
}

func (server *Server) setAgentCommissionRate(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		RateBasisPoints int `json:"rate_basis_points"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	err := server.agents.SetCommissionRate(request.Context(), request.PathValue("agentID"), input.RateBasisPoints, claims.Subject)
	if errors.Is(err, agentapp.ErrInvalidCommissionRate) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to set commission rate"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (server *Server) agentRelation(writer http.ResponseWriter, request *http.Request) {
	if server.agents == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "agent service is unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok || claims.Subject == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	relation, err := server.agents.GetRelation(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read agent relation"})
		return
	}
	writeJSON(writer, http.StatusOK, relation)
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
