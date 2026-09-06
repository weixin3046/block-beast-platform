package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
	"github.com/google/uuid"
)

func decodeStrictConfig(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

type spinConfigWriter interface {
	SaveSpinConfig(context.Context, credit.SpinConfig) (credit.SpinConfig, error)
}
type taskConfigWriter interface {
	SaveBetTaskConfig(context.Context, task.BetTaskConfig) (task.BetTaskConfig, error)
}

// IDs belong to the server: update IDs are accepted only from the resource path.
func decodeConfigInput(w http.ResponseWriter, r *http.Request, target any) bool {
	var raw map[string]json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&raw) != nil || raw == nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return false
	}
	for _, key := range []string{"id", "code", "items"} {
		if _, ok := raw[key]; ok {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return false
		}
	}
	var required []string
	switch target.(type) {
	case *credit.SpinConfig:
		required = []string{"title", "enabled", "cost_currency", "cost_minor", "sort_order", "prizes"}
	case *task.BetTaskConfig:
		if _, ok := raw["max_complete_count"]; !ok {
			raw["max_complete_count"] = json.RawMessage("1")
		}
		required = []string{"accumulation_currency", "threshold_minor", "enabled"}
		if _, ok := raw["rewards"]; !ok {
			required = append(required, "reward_currency", "reward_minor")
		}
		if _, ok := raw["rewards"]; ok {
			if _, dup := raw["reward_currency"]; dup {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return false
			}
			if _, dup := raw["reward_minor"]; dup {
				writeJSON(w, 400, map[string]string{"error": "invalid request body"})
				return false
			}
		}
	}
	for _, key := range required {
		value, ok := raw[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return false
		}
	}
	data, _ := json.Marshal(raw)
	// Reject unknown fields (including the removed business-code input).
	if err := decodeStrictConfig(data, target); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return false
	}
	return true
}

func (s *Server) saveSpinConfig(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.credits.(spinConfigWriter)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input credit.SpinConfig
	if !decodeConfigInput(w, r, &input) {
		return
	}
	if r.Method == http.MethodPut {
		input.ID = r.PathValue("spinID")
		if _, err := uuid.Parse(input.ID); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return
		}
	}
	result, err := svc.SaveSpinConfig(r.Context(), input)
	if errors.Is(err, credit.ErrSpinConfigNotFound) {
		writeJSON(w, 404, map[string]string{"error": "activity is unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid spin configuration"})
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, result)
}

func (s *Server) saveTaskConfig(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.tasks.(taskConfigWriter)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "task service is unavailable"})
		return
	}
	var input task.BetTaskConfig
	if !decodeConfigInput(w, r, &input) {
		return
	}
	if r.Method == http.MethodPut {
		input.ID = r.PathValue("taskID")
		if _, err := uuid.Parse(input.ID); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid request body"})
			return
		}
	}
	result, err := svc.SaveBetTaskConfig(r.Context(), input)
	if errors.Is(err, task.ErrTaskConfigNotFound) {
		writeJSON(w, 404, map[string]string{"error": "task config not found"})
		return
	}
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, result)
}
