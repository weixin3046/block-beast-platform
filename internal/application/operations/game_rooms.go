package operations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInvalidGameRoom = errors.New("room code, name and positive payout multiplier are required")
var ErrGameRoomNotFound = errors.New("game room not found")
var ErrGameRoomConflict = errors.New("game room code already exists")

type GameRoom struct {
	ID               string     `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	PayoutMultiplier int64      `json:"payout_multiplier"`
	SortOrder        int        `json:"sort_order"`
	GameTypes        []GameType `json:"game_types"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type GameRoomInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	PayoutMultiplier int64  `json:"payout_multiplier"`
	SortOrder        int    `json:"sort_order"`
}

func (service *Service) ListGameRooms(ctx context.Context, enabledOnly bool) ([]GameRoom, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,code,name,enabled,payout_multiplier,sort_order,created_at,updated_at
		FROM game_rooms WHERE NOT $1 OR enabled=true
		ORDER BY sort_order,created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]GameRoom, 0)
	for rows.Next() {
		var room GameRoom
		if err := rows.Scan(&room.ID, &room.Code, &room.Name, &room.Enabled, &room.PayoutMultiplier, &room.SortOrder, &room.CreatedAt, &room.UpdatedAt); err != nil {
			return nil, err
		}
		room.GameTypes = make([]GameType, 0)
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	types, err := service.ListGameTypes(ctx)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(rooms))
	for i := range rooms {
		index[rooms[i].ID] = i
	}
	for _, item := range types {
		if position, ok := index[item.RoomID]; ok && (!enabledOnly || item.Enabled) {
			rooms[position].GameTypes = append(rooms[position].GameTypes, item)
		}
	}
	return rooms, nil
}

func (service *Service) CreateGameRoom(ctx context.Context, input GameRoomInput) (GameRoom, error) {
	if err := validateGameRoom(input); err != nil {
		return GameRoom{}, err
	}
	var room GameRoom
	err := service.pool.QueryRow(ctx, `
		INSERT INTO game_rooms(id,code,name,enabled,payout_multiplier,sort_order)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id::text,code,name,enabled,payout_multiplier,sort_order,created_at,updated_at`,
		uuid.NewString(), strings.TrimSpace(input.Code), strings.TrimSpace(input.Name),
		input.Enabled, input.PayoutMultiplier, input.SortOrder).
		Scan(&room.ID, &room.Code, &room.Name, &room.Enabled, &room.PayoutMultiplier, &room.SortOrder, &room.CreatedAt, &room.UpdatedAt)
	if isRoomUniqueViolation(err) {
		return GameRoom{}, ErrGameRoomConflict
	}
	room.GameTypes = make([]GameType, 0)
	return room, err
}

func (service *Service) UpdateGameRoom(ctx context.Context, id string, input GameRoomInput) (GameRoom, error) {
	if err := validateGameRoom(input); err != nil {
		return GameRoom{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return GameRoom{}, err
	}
	defer tx.Rollback(ctx)
	var room GameRoom
	err = tx.QueryRow(ctx, `
		UPDATE game_rooms SET code=$2,name=$3,enabled=$4,payout_multiplier=$5,
			sort_order=$6,updated_at=now()
		WHERE id=$1
		RETURNING id::text,code,name,enabled,payout_multiplier,sort_order,created_at,updated_at`,
		id, strings.TrimSpace(input.Code), strings.TrimSpace(input.Name), input.Enabled,
		input.PayoutMultiplier, input.SortOrder).
		Scan(&room.ID, &room.Code, &room.Name, &room.Enabled, &room.PayoutMultiplier, &room.SortOrder, &room.CreatedAt, &room.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GameRoom{}, ErrGameRoomNotFound
	}
	if isRoomUniqueViolation(err) {
		return GameRoom{}, ErrGameRoomConflict
	}
	if err != nil {
		return GameRoom{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE game_types
		SET rules=jsonb_set(
			jsonb_set(rules,'{payout_multiplier}',to_jsonb($2::bigint)),
			'{payout_divisor}','100'::jsonb
		),updated_at=now()
		WHERE room_id=$1`, id, input.PayoutMultiplier); err != nil {
		return GameRoom{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return GameRoom{}, err
	}
	room.GameTypes = make([]GameType, 0)
	return room, nil
}

func validateGameRoom(input GameRoomInput) error {
	if !gameCodePattern.MatchString(strings.TrimSpace(input.Code)) ||
		strings.TrimSpace(input.Name) == "" || input.PayoutMultiplier <= 0 {
		return ErrInvalidGameRoom
	}
	return nil
}

func isRoomUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
