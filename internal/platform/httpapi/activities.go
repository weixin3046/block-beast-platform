package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
)

func (server *Server) playConfiguredSpin(writer http.ResponseWriter, request *http.Request) {
	server.playSpin(writer, request, request.PathValue("spinID"))
}

func (server *Server) playSpin(writer http.ResponseWriter, request *http.Request, spinID string) {
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
	result, err := server.credits.LuckySpin(request.Context(), claims.Subject, spinID, input.RequestID)
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

func (server *Server) spinConfigs(writer http.ResponseWriter, request *http.Request) {
	items, err := server.credits.ListSpinConfigs(request.Context(), true)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list spins"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}
func (server *Server) adminSpinConfigs(writer http.ResponseWriter, request *http.Request) {
	items, err := server.credits.ListSpinConfigs(request.Context(), false)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list spins"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}
func (server *Server) replaceSpinConfigs(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Items []credit.SpinConfig `json:"items"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	items, err := server.credits.ReplaceSpinConfigs(request.Context(), input.Items)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, items)
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

func (server *Server) claimBetTask(writer http.ResponseWriter, request *http.Request) {
	if server.tasks == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "task service is unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	result, err := server.tasks.ClaimBetTask(request.Context(), claims.Subject, request.PathValue("taskID"))
	switch {
	case errors.Is(err, task.ErrTaskConfigNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, task.ErrTaskNotCompleted), errors.Is(err, task.ErrTaskAlreadyClaimed):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to claim task reward"})
	default:
		writeJSON(writer, http.StatusOK, result)
	}
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
