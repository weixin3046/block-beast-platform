package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/config"
)

type stubChatService struct {
	sendError error
	created   bool
}

func (stubChatService) OpenCustomerServiceRoom(context.Context, string) (chat.Room, error) {
	return chat.Room{ID: "room-1", Type: "customer_service"}, nil
}

func (stubChatService) ListRooms(context.Context, string, bool, int) ([]chat.Room, error) {
	return []chat.Room{{ID: "room-1"}}, nil
}

func (stubChatService) ListMessages(context.Context, string, string, bool, int) ([]chat.Message, error) {
	return []chat.Message{}, nil
}

func (stub stubChatService) SendMessage(context.Context, string, string, string, string, bool) (chat.Message, bool, error) {
	return chat.Message{ID: "message-1"}, stub.created, stub.sendError
}

func TestChatMessageEndpointMapsResults(t *testing.T) {
	tests := []struct {
		name string
		stub stubChatService
		want int
	}{
		{name: "created", stub: stubChatService{created: true}, want: http.StatusCreated},
		{name: "duplicate", stub: stubChatService{}, want: http.StatusOK},
		{name: "invalid", stub: stubChatService{sendError: chat.ErrInvalidMessage}, want: http.StatusBadRequest},
		{name: "forbidden", stub: stubChatService{sendError: chat.ErrRoomAccessDenied}, want: http.StatusForbidden},
		{name: "not found", stub: stubChatService{sendError: chat.ErrRoomNotFound}, want: http.StatusNotFound},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := New(
				config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
				nil, readinessChecker{}, nil, nil, nil, nil, WithChat(testCase.stub),
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/rooms/room-1/messages", strings.NewReader(`{"client_request_id":"request-1","body":"hello"}`))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status = %d, want %d", response.Code, testCase.want)
			}
		})
	}
}
