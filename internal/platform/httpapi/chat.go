package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/domain/identity"
)

type ChatService interface {
	OpenCustomerServiceRoom(ctx context.Context, userID string) (chat.Room, error)
	ListRooms(ctx context.Context, userID string, staff bool, limit int) ([]chat.Room, error)
	ListMessages(ctx context.Context, roomID, userID string, staff bool, limit int) ([]chat.Message, error)
	SendMessage(ctx context.Context, roomID, senderUserID, clientRequestID, body string, staff bool) (chat.Message, bool, error)
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
	room, err := server.chat.OpenCustomerServiceRoom(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to open customer service room"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, room)
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

func (server *Server) sendChatMessage(writer http.ResponseWriter, request *http.Request) {
	if server.chat == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "chat is unavailable"})
		return
	}
	var input struct {
		ClientRequestID string `json:"client_request_id"`
		Body            string `json:"body"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid chat message"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	item, created, err := server.chat.SendMessage(
		request.Context(), request.PathValue("roomID"), claims.Subject,
		input.ClientRequestID, input.Body, isStaff(claims),
	)
	if err != nil {
		server.writeChatResult(writer, request, nil, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, item)
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
