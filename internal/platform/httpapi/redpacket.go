package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/redpacket"
)

type RedPacketService interface {
	Create(ctx context.Context, input redpacket.CreateInput) (redpacket.Packet, bool, error)
	Claim(ctx context.Context, packetID, userID string) (redpacket.Claim, bool, error)
	Find(ctx context.Context, packetID, userID string) (redpacket.Packet, error)
}

func WithRedPackets(service RedPacketService) Option {
	return func(server *Server) { server.redPackets = service }
}

func (server *Server) createRedPacket(writer http.ResponseWriter, request *http.Request) {
	if server.redPackets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "red packets are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	var input struct {
		ClientRequestID string `json:"client_request_id"`
		Currency        string `json:"currency"`
		Greeting        string `json:"greeting"`
		TotalMinor      int64  `json:"total_minor"`
		PacketCount     int    `json:"packet_count"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid red packet request"})
		return
	}
	packet, created, err := server.redPackets.Create(request.Context(), redpacket.CreateInput{
		RoomID: request.PathValue("roomID"), SenderUserID: claims.Subject,
		ClientRequestID: input.ClientRequestID, Currency: input.Currency,
		Greeting: input.Greeting, TotalMinor: input.TotalMinor, PacketCount: input.PacketCount,
	})
	if writeRedPacketError(writer, err) {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, packet)
}

func (server *Server) claimRedPacket(writer http.ResponseWriter, request *http.Request) {
	if server.redPackets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "red packets are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	claim, created, err := server.redPackets.Claim(request.Context(), request.PathValue("packetID"), claims.Subject)
	if writeRedPacketError(writer, err) {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, claim)
}

func (server *Server) redPacket(writer http.ResponseWriter, request *http.Request) {
	if server.redPackets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "red packets are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	packet, err := server.redPackets.Find(request.Context(), request.PathValue("packetID"), claims.Subject)
	if writeRedPacketError(writer, err) {
		return
	}
	writeJSON(writer, http.StatusOK, packet)
}

func writeRedPacketError(writer http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, redpacket.ErrInvalidPacket):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, redpacket.ErrPacketNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, redpacket.ErrRoomAccessDenied), errors.Is(err, redpacket.ErrSenderCannotClaim):
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, redpacket.ErrPacketUnavailable), errors.Is(err, redpacket.ErrInsufficientBalance):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to process red packet"})
	}
	return true
}
