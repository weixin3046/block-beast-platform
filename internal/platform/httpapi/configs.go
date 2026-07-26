package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/operations"
)

func (server *Server) publicConfig(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "platform configs are unavailable"})
		return
	}
	item, err := server.operations.PublicConfig(request.Context(), request.PathValue("key"))
	if errors.Is(err, operations.ErrConfigNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read platform config"})
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (server *Server) adminConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "platform configs are unavailable"})
		return
	}
	items, err := server.operations.ListConfigs(request.Context(), request.URL.Query().Get("visibility"), queryLimit(request, 100))
	if errors.Is(err, operations.ErrInvalidConfig) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list platform configs"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) putConfig(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "platform configs are unavailable"})
		return
	}
	var input operations.ConfigInput
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid platform config"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	item, err := server.operations.PutConfig(request.Context(), request.PathValue("key"), claims.Subject, input)
	switch {
	case errors.Is(err, operations.ErrInvalidConfig):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrConfigNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrConfigVersionConflict):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update platform config"})
	default:
		server.recordAudit(request.Context(), audit.Entry{
			ActorUserID: claims.Subject, Action: "platform_config.put",
			TargetType: "platform_config", TargetID: item.Key,
			Payload: map[string]any{"visibility": item.Visibility, "version": item.Version},
		})
		writeJSON(writer, http.StatusOK, item)
	}
}
