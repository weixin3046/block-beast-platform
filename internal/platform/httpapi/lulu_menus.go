package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/operations"
)

type LuluMenuService interface {
	GetLuluMenus(context.Context) (operations.LuluMenus, error)
	UpdateLuluRoomPlayConfig(context.Context, operations.LuluRoomPlayConfigUpdate) (operations.LuluMenus, error)
	UpdateLuluRoomPlayConfigs(context.Context, []operations.LuluRoomPlayConfigUpdate) (operations.LuluMenus, error)
	UpdateLuluPrimeTimeConfig(context.Context, operations.LuluPrimeTimeConfigUpdate) ([]operations.LuluPrimeTimeConfigUpdate, error)
	UpdateLuluPrimeTimeConfigs(context.Context, []operations.LuluPrimeTimeConfigUpdate) ([]operations.LuluPrimeTimeConfigUpdate, error)
	GetLuluPrimeTimeConfigs(context.Context) ([]operations.LuluPrimeTimeConfigUpdate, error)
}

func (server *Server) updateLuluRoomPlayConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	var input operations.LuluRoomPlayConfigsUpdate
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	result, err := server.luluMenus.UpdateLuluRoomPlayConfigs(request.Context(), input.Configs)
	switch {
	case errors.Is(err, operations.ErrInvalidLuluPlayConfig):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrLuluPlayConfigNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update lulu room play configuration"})
	default:
		writeJSON(writer, http.StatusOK, result)
	}
}

func (server *Server) updateLuluRoomPlayConfig(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	var input operations.LuluRoomPlayConfigUpdate
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	result, err := server.luluMenus.UpdateLuluRoomPlayConfig(request.Context(), input)
	switch {
	case errors.Is(err, operations.ErrInvalidLuluPlayConfig):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrLuluPlayConfigNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update lulu room play configuration"})
	default:
		writeJSON(writer, http.StatusOK, result)
	}
}

func WithLuluMenus(service LuluMenuService) Option {
	return func(server *Server) { server.luluMenus = service }
}

func writeLuluPrimeTimeError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operations.ErrLuluPrimeTimeConfigInvalid):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrLuluPrimeTimeConfigNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update lulu prime time configuration"})
	}
}

func (server *Server) adminLuluPrimeTimeConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	result, err := server.luluMenus.GetLuluPrimeTimeConfigs(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to load lulu prime time configuration"})
		return
	}
	if result == nil {
		result = []operations.LuluPrimeTimeConfigUpdate{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": result})
}

func (server *Server) updateLuluPrimeTimeConfigs(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	var input operations.LuluPrimeTimeConfigsUpdate
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	result, err := server.luluMenus.UpdateLuluPrimeTimeConfigs(request.Context(), input.Configs)
	if err != nil {
		writeLuluPrimeTimeError(writer, err)
		return
	}
	if result == nil {
		result = []operations.LuluPrimeTimeConfigUpdate{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": result})
}

func (server *Server) updateLuluPrimeTimeConfig(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	var input operations.LuluPrimeTimeConfigUpdate
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	result, err := server.luluMenus.UpdateLuluPrimeTimeConfig(request.Context(), input)
	if err != nil {
		writeLuluPrimeTimeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": result})
}

func (server *Server) luluMenuConfig(writer http.ResponseWriter, request *http.Request) {
	if server.luluMenus == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "lulu menus are unavailable"})
		return
	}
	result, err := server.luluMenus.GetLuluMenus(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to load lulu menus"})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
