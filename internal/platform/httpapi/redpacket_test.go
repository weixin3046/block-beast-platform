package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/redpacket"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
)

type stubRedPacketService struct {
	err     error
	created bool
}

func (stub stubRedPacketService) Create(context.Context, redpacket.CreateInput) (redpacket.Packet, bool, error) {
	return redpacket.Packet{ID: "packet-1"}, stub.created, stub.err
}

func (stub stubRedPacketService) Claim(context.Context, string, string) (redpacket.Claim, bool, error) {
	return redpacket.Claim{ID: "claim-1"}, stub.created, stub.err
}

func (stub stubRedPacketService) Find(context.Context, string, string) (redpacket.Packet, error) {
	return redpacket.Packet{ID: "packet-1"}, stub.err
}

func TestCreateRedPacketMapsTransactionErrors(t *testing.T) {
	tests := []struct {
		stub stubRedPacketService
		want int
	}{
		{stub: stubRedPacketService{created: true}, want: http.StatusCreated},
		{stub: stubRedPacketService{}, want: http.StatusOK},
		{stub: stubRedPacketService{err: redpacket.ErrInvalidPacket}, want: http.StatusBadRequest},
		{stub: stubRedPacketService{err: redpacket.ErrRoomAccessDenied}, want: http.StatusForbidden},
		{stub: stubRedPacketService{err: redpacket.ErrInsufficientBalance}, want: http.StatusConflict},
	}
	for _, testCase := range tests {
		server := New(
			config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil,
			WithAuth(NewAuthenticator(testSecret)), WithRedPackets(testCase.stub),
		)
		request := httptest.NewRequest(
			http.MethodPost, "/v1/chat/rooms/room-1/red-packets",
			strings.NewReader(`{"client_request_id":"r1","currency":"USDT","total_minor":10,"packet_count":2}`),
		)
		request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != testCase.want {
			t.Fatalf("error %v status = %d, want %d", testCase.stub.err, response.Code, testCase.want)
		}
	}
}
