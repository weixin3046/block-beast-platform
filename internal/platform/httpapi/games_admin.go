package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/operations"
)

type GameAdminService interface {
	ListGameTypes(ctx context.Context) ([]operations.GameType, error)
	CreateGameType(ctx context.Context, input operations.GameTypeInput) (operations.GameType, error)
	UpdateGameType(ctx context.Context, id string, input operations.GameTypeInput) (operations.GameType, error)
	ListRounds(ctx context.Context, gameTypeCode, status string, limit int) ([]operations.ManagedRound, error)
	CreateRound(ctx context.Context, gameTypeID string, betClosesAt time.Time) (operations.ManagedRound, error)
}

type GameRoomService interface {
	ListGameRooms(ctx context.Context, enabledOnly bool) ([]operations.GameRoom, error)
	CreateGameRoom(ctx context.Context, input operations.GameRoomInput) (operations.GameRoom, error)
	UpdateGameRoom(ctx context.Context, id string, input operations.GameRoomInput) (operations.GameRoom, error)
}

func WithGameAdmin(service GameAdminService) Option {
	return func(server *Server) { server.gameAdmin = service }
}

func WithGameRooms(service GameRoomService) Option {
	return func(server *Server) { server.gameRoomAdmin = service }
}

func (server *Server) gameRooms(writer http.ResponseWriter, request *http.Request) {
	items, err := server.gameRoomAdmin.ListGameRooms(request.Context(), true)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list game rooms"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) adminGameRooms(writer http.ResponseWriter, request *http.Request) {
	items, err := server.gameRoomAdmin.ListGameRooms(request.Context(), false)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list game rooms"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) createGameRoom(writer http.ResponseWriter, request *http.Request) {
	var input operations.GameRoomInput
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.gameRoomAdmin.CreateGameRoom(request.Context(), input)
	server.writeGameRoomResult(writer, request, item, err, http.StatusCreated, "game_room.create")
}

func (server *Server) updateGameRoom(writer http.ResponseWriter, request *http.Request) {
	var input operations.GameRoomInput
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.gameRoomAdmin.UpdateGameRoom(request.Context(), request.PathValue("roomID"), input)
	server.writeGameRoomResult(writer, request, item, err, http.StatusOK, "game_room.update")
}

func (server *Server) writeGameRoomResult(writer http.ResponseWriter, request *http.Request, item operations.GameRoom, err error, status int, action string) {
	switch {
	case errors.Is(err, operations.ErrInvalidGameRoom):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameRoomNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameRoomConflict):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to save game room"})
	default:
		claims, _ := ClaimsFromContext(request.Context())
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: action, TargetType: "game_room", TargetID: item.ID})
		writeJSON(writer, status, item)
	}
}

func (server *Server) adminGameTypes(writer http.ResponseWriter, request *http.Request) {
	items, err := server.gameAdmin.ListGameTypes(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list game types"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) createGameType(writer http.ResponseWriter, request *http.Request) {
	var input operations.GameTypeInput
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.gameAdmin.CreateGameType(request.Context(), input)
	writeGameTypeResult(server, writer, request, item, err, http.StatusCreated, "game_type.create")
}

func (server *Server) updateGameType(writer http.ResponseWriter, request *http.Request) {
	var input operations.GameTypeInput
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.gameAdmin.UpdateGameType(request.Context(), request.PathValue("gameTypeID"), input)
	writeGameTypeResult(server, writer, request, item, err, http.StatusOK, "game_type.update")
}

func (server *Server) adminRounds(writer http.ResponseWriter, request *http.Request) {
	items, err := server.gameAdmin.ListRounds(request.Context(), request.URL.Query().Get("game_type"), request.URL.Query().Get("status"), queryLimit(request, 100))
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list rounds"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) createRound(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		GameTypeID  string    `json:"game_type_id"`
		BetClosesAt time.Time `json:"bet_closes_at"`
	}
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.gameAdmin.CreateRound(request.Context(), input.GameTypeID, input.BetClosesAt)
	switch {
	case errors.Is(err, operations.ErrInvalidRound):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameTypeNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameRoomNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to create round"})
	default:
		claims, _ := ClaimsFromContext(request.Context())
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "round.create", TargetType: "round", TargetID: item.ID, Payload: map[string]any{"game_type_id": item.GameTypeID, "sequence": item.Sequence}})
		writeJSON(writer, http.StatusCreated, item)
	}
}

func decodeStrictJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return err
	}
	return nil
}

func writeGameTypeResult(server *Server, writer http.ResponseWriter, request *http.Request, item operations.GameType, err error, successStatus int, action string) {
	switch {
	case errors.Is(err, operations.ErrInvalidGameType):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameTypeNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, operations.ErrGameTypeConflict):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to save game type"})
	default:
		claims, _ := ClaimsFromContext(request.Context())
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: action, TargetType: "game_type", TargetID: item.ID})
		writeJSON(writer, successStatus, item)
	}
}
