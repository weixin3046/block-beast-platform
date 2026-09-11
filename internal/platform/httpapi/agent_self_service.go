package httpapi

import (
	"errors"
	agentapp "github.com/block-beast/platform/internal/application/agent"
	"net/http"
	"strconv"
)

func (s *Server) agentIncomeSummary(w http.ResponseWriter, r *http.Request) {
	c, ok := ClaimsFromContext(r.Context())
	if !ok || c.Subject == "" {
		writeJSON(w, 401, map[string]string{"error": "authentication required"})
		return
	}
	if s.agents == nil {
		writeJSON(w, 503, map[string]string{"error": "agent service is unavailable"})
		return
	}
	out, err := s.agents.IncomeSummary(r.Context(), c.Subject)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "unable to list commissions"})
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) setDirectPlayerLevel(w http.ResponseWriter, r *http.Request) {
	c, ok := ClaimsFromContext(r.Context())
	if !ok || c.Subject == "" {
		writeJSON(w, 401, map[string]string{"error": "authentication required"})
		return
	}
	target, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil || target < 10001 {
		writeJSON(w, 400, map[string]string{"error": "invalid user id"})
		return
	}
	var in struct {
		Level *int `json:"agent_level"`
	}
	if !decodeSecurity(w, r, &in) {
		return
	}
	if in.Level == nil {
		writeJSON(w, 400, map[string]string{"error": agentapp.ErrChildLevelInvalid.Error()})
		return
	}
	if s.agents == nil {
		writeJSON(w, 503, map[string]string{"error": "agent service is unavailable"})
		return
	}
	err = s.agents.SetDirectPlayerLevel(r.Context(), c.Subject, target, *in.Level)
	switch {
	case errors.Is(err, agentapp.ErrChildLevelInvalid):
		writeJSON(w, 400, map[string]string{"error": err.Error()})
	case errors.Is(err, agentapp.ErrChildLevelForbidden):
		writeJSON(w, 403, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, 500, map[string]string{"error": "unable to set agent level"})
	default:
		writeJSON(w, 200, map[string]any{"user_id": target, "agent_level": *in.Level})
	}
}
