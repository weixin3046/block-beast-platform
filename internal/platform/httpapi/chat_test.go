package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/config"
)

type stubChatService struct{}

func (stubChatService) DeleteMessage(context.Context, string, string, string) error { return nil }

func (stubChatService) OpenCustomerServiceRooms(context.Context, string) (chat.CustomerServiceRooms, error) {
	return chat.CustomerServiceRooms{Deposit: chat.Room{ID: "room-1", Type: "customer_service", ServiceType: chat.ServiceTypeDeposit}, Withdrawal: chat.Room{ID: "room-2", Type: "customer_service", ServiceType: chat.ServiceTypeWithdrawal}}, nil
}

func (stubChatService) ListRooms(context.Context, string, bool, chat.RoomQuery) ([]chat.Room, error) {
	return []chat.Room{{ID: "room-1"}}, nil
}

func (stubChatService) ListMessages(context.Context, string, string, bool, int) ([]chat.Message, error) {
	return []chat.Message{}, nil
}

type roomQueryStub struct {
	stubChatService
	query chat.RoomQuery
}

func (s *roomQueryStub) ListRooms(_ context.Context, _ string, _ bool, query chat.RoomQuery) ([]chat.Room, error) {
	s.query = query
	if query.ServiceType == "bad" {
		return nil, chat.ErrInvalidRoomQuery
	}
	return []chat.Room{{ID: "room-1"}}, nil
}

func TestOpenCustomerServiceRoomsReturnsBothRooms(t *testing.T) {
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil, WithChat(stubChatService{}),
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/customer-service", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var rooms chat.CustomerServiceRooms
	if err := json.NewDecoder(response.Body).Decode(&rooms); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rooms.Deposit.ServiceType != chat.ServiceTypeDeposit || rooms.Withdrawal.ServiceType != chat.ServiceTypeWithdrawal {
		t.Fatalf("service types = %+v", rooms)
	}
}

func TestChatRoomsPassesCustomerServiceFilters(t *testing.T) {
	service := &roomQueryStub{}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithChat(service))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/chat/rooms?service_type=deposit&q=10110&has_messages=true&limit=20", nil))
	if response.Code != http.StatusOK ||
		service.query.ServiceType != chat.ServiceTypeDeposit ||
		service.query.Search != "10110" ||
		!service.query.HasMessages ||
		service.query.Limit != 20 {
		t.Fatalf("status=%d query=%+v", response.Code, service.query)
	}
}

func TestChatRoomsRejectsInvalidCustomerServiceType(t *testing.T) {
	service := &roomQueryStub{}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithChat(service))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/chat/rooms?service_type=bad", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

type deleteChatStub struct {
	stubChatService
	err error
}

func (s deleteChatStub) DeleteMessage(context.Context, string, string, string) error { return s.err }
func TestDeleteChatMessageHTTP(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 204}, {chat.ErrInvalidChatID, 400}, {chat.ErrMessageDeleteDenied, 403}, {chat.ErrRoomAccessDenied, 403}, {chat.ErrMessageNotFound, 404}, {chat.ErrRoomNotFound, 404}} {
		server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithChat(deleteChatStub{err: tc.err}))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/chat/rooms/room/messages/message", nil))
		if response.Code != tc.status {
			t.Fatalf("%v: status %d, want %d", tc.err, response.Code, tc.status)
		}
	}
}

func TestDeleteChatMessageRequiresAuthentication(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithChat(stubChatService{}), WithAuth(NewAuthenticator(testSecret)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/chat/rooms/room/messages/message", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", response.Code)
	}
}
