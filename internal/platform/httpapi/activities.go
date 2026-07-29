package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
)

func (server *Server) luckySpin(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.RequestID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	result, err := server.credits.LuckySpin(request.Context(), claims.Subject, input.RequestID)
	switch {
	case errors.Is(err, credit.ErrActivityUnavailable):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, credit.ErrInsufficientStamina):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to play lucky spin"})
	default:
		writeJSON(writer, http.StatusOK, result)
	}
}

func (server *Server) betTasks(writer http.ResponseWriter, request *http.Request) {
	if server.tasks == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "task service is unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	items, err := server.tasks.BetTasks(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list bet tasks"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) adminBetTaskConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.tasks == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "task service is unavailable"})
		return
	}
	items, err := server.tasks.BetTaskConfigs(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list task configs"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) replaceBetTaskConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.tasks == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "task service is unavailable"})
		return
	}
	var input struct {
		Items []task.BetTaskConfig `json:"items"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	items, err := server.tasks.ReplaceBetTaskConfigs(request.Context(), input.Items)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}
