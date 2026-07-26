package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/operations"
)

type OperationsService interface {
	ListAnnouncements(ctx context.Context, activeOnly bool, limit int) ([]operations.Announcement, error)
	CreateAnnouncement(ctx context.Context, input operations.AnnouncementInput) (operations.Announcement, error)
	UpdateAnnouncement(ctx context.Context, announcementID string, input operations.AnnouncementInput) (operations.Announcement, error)
	ListAuditLogs(ctx context.Context, action, actorUserID string, limit int) ([]operations.AuditLog, error)
	PublicConfig(ctx context.Context, key string) (operations.PlatformConfig, error)
	ListConfigs(ctx context.Context, visibility string, limit int) ([]operations.PlatformConfig, error)
	PutConfig(ctx context.Context, key, actorUserID string, input operations.ConfigInput) (operations.PlatformConfig, error)
}

func WithOperations(service OperationsService) Option {
	return func(server *Server) { server.operations = service }
}

func (server *Server) announcements(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "announcements are unavailable"})
		return
	}
	items, err := server.operations.ListAnnouncements(request.Context(), true, queryLimit(request, 50))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list announcements"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) adminAnnouncements(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "announcements are unavailable"})
		return
	}
	items, err := server.operations.ListAnnouncements(request.Context(), false, queryLimit(request, 50))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list announcements"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) createAnnouncement(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "announcements are unavailable"})
		return
	}
	input, ok := decodeAnnouncement(writer, request)
	if !ok {
		return
	}
	item, err := server.operations.CreateAnnouncement(request.Context(), input)
	if errors.Is(err, operations.ErrInvalidAnnouncement) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to create announcement"})
		return
	}
	server.auditAnnouncement(request, "announcement.create", item.ID)
	writeJSON(writer, http.StatusCreated, item)
}

func (server *Server) updateAnnouncement(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "announcements are unavailable"})
		return
	}
	input, ok := decodeAnnouncement(writer, request)
	if !ok {
		return
	}
	item, err := server.operations.UpdateAnnouncement(request.Context(), request.PathValue("announcementID"), input)
	switch {
	case errors.Is(err, operations.ErrInvalidAnnouncement):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrAnnouncementNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to update announcement"})
	default:
		server.auditAnnouncement(request, "announcement.update", item.ID)
		writeJSON(writer, http.StatusOK, item)
	}
}

func (server *Server) auditLogs(writer http.ResponseWriter, request *http.Request) {
	if server.operations == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "audit logs are unavailable"})
		return
	}
	items, err := server.operations.ListAuditLogs(request.Context(), request.URL.Query().Get("action"), request.URL.Query().Get("actor_user_id"), queryLimit(request, 100))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list audit logs"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func decodeAnnouncement(writer http.ResponseWriter, request *http.Request) (operations.AnnouncementInput, bool) {
	var input operations.AnnouncementInput
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid announcement"})
		return operations.AnnouncementInput{}, false
	}
	return input, true
}

func queryLimit(request *http.Request, fallback int) int {
	limit, err := strconv.Atoi(request.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return fallback
	}
	return limit
}

func (server *Server) auditAnnouncement(request *http.Request, action, targetID string) {
	claims, _ := ClaimsFromContext(request.Context())
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: action, TargetType: "announcement", TargetID: targetID})
}
