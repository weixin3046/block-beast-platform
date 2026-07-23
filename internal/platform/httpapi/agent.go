package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	agentapp "github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/application/audit"
)

type AgentService interface {
	Bind(ctx context.Context, userID, parentID string) error
	GetRelation(ctx context.Context, userID string) (agentapp.Relation, error)
	SetCommissionRate(ctx context.Context, agentID string, rateBasisPoints int, operatorID string) error
	ListCommissions(ctx context.Context, agentID string, limit int) ([]agentapp.Commission, error)
	ListAllCommissions(ctx context.Context, status string, limit int) ([]agentapp.Commission, error)
	TeamSummary(ctx context.Context, agentID string) (agentapp.TeamSummary, error)
	ReverseCommission(ctx context.Context, commissionID string) error
	GrantCommission(ctx context.Context, requestID, agentID, currency string, amount int64, remark, operatorID string) (string, error)
}

func (server *Server) grantCommission(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		RequestID   string `json:"request_id"`
		Currency    string `json:"currency"`
		AmountMinor int64  `json:"amount_minor"`
		Remark      string `json:"remark"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	id, err := server.agents.GrantCommission(request.Context(), input.RequestID, request.PathValue("agentID"), input.Currency, input.AmountMinor, input.Remark, claims.Subject)
	if errors.Is(err, agentapp.ErrInvalidCommissionAdjustment) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to grant commission"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{
		ActorUserID: claims.Subject,
		Action:      "commission.grant",
		TargetType:  "commission_adjustment",
		TargetID:    id,
		Payload:     map[string]any{"agent_id": request.PathValue("agentID"), "currency": input.Currency, "amount_minor": input.AmountMinor},
	})
	writeJSON(writer, http.StatusCreated, map[string]string{"adjustment_id": id})
}

func (server *Server) teamSummary(writer http.ResponseWriter, request *http.Request) {
	claims, ok := ClaimsFromContext(request.Context())
	if !ok || claims.Subject == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	summary, err := server.agents.TeamSummary(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read team summary"})
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}

func (server *Server) adminCommissions(writer http.ResponseWriter, request *http.Request) {
	items, err := server.agents.ListAllCommissions(request.Context(), request.URL.Query().Get("status"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list commissions"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) reverseCommission(writer http.ResponseWriter, request *http.Request) {
	claims, _ := ClaimsFromContext(request.Context())
	err := server.agents.ReverseCommission(request.Context(), request.PathValue("commissionID"))
	switch {
	case errors.Is(err, agentapp.ErrCommissionNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, agentapp.ErrCommissionState), errors.Is(err, agentapp.ErrInsufficientCommissionBalance):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to reverse commission"})
	default:
		server.recordAudit(request.Context(), audit.Entry{
			ActorUserID: claims.Subject,
			Action:      "commission.reverse",
			TargetType:  "commission",
			TargetID:    request.PathValue("commissionID"),
		})
		writeJSON(writer, http.StatusOK, map[string]string{"status": "reversed"})
	}
}

func (server *Server) commissions(writer http.ResponseWriter, request *http.Request) {
	claims, ok := ClaimsFromContext(request.Context())
	if !ok || claims.Subject == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	items, err := server.agents.ListCommissions(request.Context(), claims.Subject, 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list commissions"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
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
