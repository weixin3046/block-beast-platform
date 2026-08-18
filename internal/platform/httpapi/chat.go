package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/domain/identity"
)

type ChatService interface {
	OpenCustomerServiceRooms(ctx context.Context, userID string) (chat.CustomerServiceRooms, error)
	ListRooms(ctx context.Context, userID string, staff bool, limit int) ([]chat.Room, error)
	ListMessages(ctx context.Context, roomID, userID string, staff bool, limit int) ([]chat.Message, error)
}

func WithChat(service ChatService) Option {
	return func(server *Server) { server.chat = service }
}

func (server *Server) openCustomerServiceRoom(writer http.ResponseWriter, request *http.Request) {
	if server.chat == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chat is unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	rooms, err := server.chat.OpenCustomerServiceRooms(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to open customer service room"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, rooms)
}

func (server *Server) chatRooms(writer http.ResponseWriter, request *http.Request) {
	if server.chat == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chat is unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	items, err := server.chat.ListRooms(request.Context(), claims.Subject, isStaff(claims), queryLimit(request, 50))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list chat rooms"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, items)
}

func (server *Server) chatMessages(writer http.ResponseWriter, request *http.Request) {
	if server.chat == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chat is unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	items, err := server.chat.ListMessages(request.Context(), request.PathValue("roomID"), claims.Subject, isStaff(claims), queryLimit(request, 50))
	server.writeChatResult(writer, request, items, err)
}

func (server *Server) writeChatResult(writer http.ResponseWriter, request *http.Request, result any, err error) {
	switch {
	case errors.Is(err, chat.ErrInvalidMessage), errors.Is(err, chat.ErrInvalidRequestID):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, chat.ErrRoomAccessDenied):
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, chat.ErrRoomNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to process chat request"})
	default:
		server.writePublicJSON(writer, request, http.StatusOK, result)
	}
}

func isStaff(claims identity.AccessTokenClaims) bool {
	return claims.HasRole(identity.RoleAdmin, identity.RoleOperator)
}
