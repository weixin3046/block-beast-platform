package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInvalidGameType = errors.New("game name, mode and valid rules are required")
var ErrGameTypeNotFound = errors.New("game type not found")
var ErrGameTypeConflict = errors.New("game code already exists")
var ErrInvalidRound = errors.New("game type and future bet close time are required")

type GameType struct {
	ID              string          `json:"id"`
	RoomID          string          `json:"room_id,omitempty"`
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Mode            string          `json:"mode,omitempty"`
	BlockInterval   int             `json:"block_interval,omitempty"`
	CloseBeforeSecs int             `json:"close_before_seconds"`
	Enabled         bool            `json:"enabled"`
	Rules           json.RawMessage `json:"rules"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type GameTypeInput struct {
	RoomID          string          `json:"room_id,omitempty"`
	Name            string          `json:"name"`
	Mode            string          `json:"mode,omitempty"`
	BlockInterval   int             `json:"block_interval,omitempty"`
	CloseBeforeSecs int             `json:"close_before_seconds"`
	Enabled         bool            `json:"enabled"`
	Rules           json.RawMessage `json:"rules"`
}

type ManagedRound struct {
	ID           string          `json:"id"`
	GameTypeID   string          `json:"game_type_id"`
	GameTypeCode string          `json:"game_type_code"`
	Sequence     int64           `json:"sequence"`
	Status       string          `json:"status"`
	BetClosesAt  time.Time       `json:"bet_closes_at"`
	ResultAt     time.Time       `json:"result_at"`
	Outcome      json.RawMessage `json:"outcome,omitempty"`
	SettledAt    *time.Time      `json:"settled_at,omitempty"`
}

func (service *Service) ListGameTypes(ctx context.Context) ([]GameType, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text,COALESCE(room_id::text,''),code,name,COALESCE(mode,''),COALESCE(block_interval,0),close_before_seconds,enabled,rules,created_at,updated_at FROM game_types ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]GameType, 0)
	for rows.Next() {
		var item GameType
		if err := rows.Scan(&item.ID, &item.RoomID, &item.Code, &item.Name, &item.Mode, &item.BlockInterval, &item.CloseBeforeSecs, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) CreateGameType(ctx context.Context, input GameTypeInput) (GameType, error) {
	var err error
	input.Rules, err = service.applyRoomPayout(ctx, input.RoomID, input.Rules)
	if err != nil {
		return GameType{}, err
	}
	if err := validateGameType(input); err != nil {
		return GameType{}, err
	}
	var item GameType
	gameCode := generatedCode("play")
	err = service.pool.QueryRow(ctx, `
		INSERT INTO game_types (id,room_id,code,name,mode,block_interval,close_before_seconds,enabled,rules)
		VALUES ($1,NULLIF($2,'')::uuid,$3,$4,NULLIF($5,''),NULLIF($6,0),$7,$8,$9)
		RETURNING id::text,COALESCE(room_id::text,''),code,name,COALESCE(mode,''),COALESCE(block_interval,0),close_before_seconds,enabled,rules,created_at,updated_at`,
		uuid.NewString(), input.RoomID, gameCode, strings.TrimSpace(input.Name),
		strings.TrimSpace(input.Mode), input.BlockInterval, normalizeGameCloseBeforeSecs(input.CloseBeforeSecs), input.Enabled, input.Rules).
		Scan(&item.ID, &item.RoomID, &item.Code, &item.Name, &item.Mode, &item.BlockInterval, &item.CloseBeforeSecs, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt)
	if isUniqueViolation(err) {
		return GameType{}, ErrGameTypeConflict
	}
	return item, err
}

func (service *Service) UpdateGameType(ctx context.Context, id string, input GameTypeInput) (GameType, error) {
	var err error
	input.Rules, err = service.applyRoomPayout(ctx, input.RoomID, input.Rules)
	if err != nil {
		return GameType{}, err
	}
	if err := validateGameType(input); err != nil {
		return GameType{}, err
	}
	var item GameType
	err = service.pool.QueryRow(ctx, `
		UPDATE game_types SET room_id=NULLIF($2,'')::uuid,name=$3,
			mode=NULLIF($4,''),block_interval=NULLIF($5,0),close_before_seconds=$6,enabled=$7,rules=$8,updated_at=now()
		WHERE id=$1
		RETURNING id::text,COALESCE(room_id::text,''),code,name,COALESCE(mode,''),COALESCE(block_interval,0),close_before_seconds,enabled,rules,created_at,updated_at`,
		id, input.RoomID, strings.TrimSpace(input.Name),
		strings.TrimSpace(input.Mode), input.BlockInterval, normalizeGameCloseBeforeSecs(input.CloseBeforeSecs), input.Enabled, input.Rules).
		Scan(&item.ID, &item.RoomID, &item.Code, &item.Name, &item.Mode, &item.BlockInterval, &item.CloseBeforeSecs, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GameType{}, ErrGameTypeNotFound
	}
	if isUniqueViolation(err) {
		return GameType{}, ErrGameTypeConflict
	}
	return item, err
}

func (service *Service) applyRoomPayout(ctx context.Context, roomID string, raw json.RawMessage) (json.RawMessage, error) {
	if roomID == "" {
		return raw, nil
	}
	var gameKind string
	if err := service.pool.QueryRow(ctx, `SELECT game_kind FROM game_rooms WHERE id=$1`, roomID).Scan(&gameKind); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGameRoomNotFound
	} else if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, ErrInvalidGameType
	}
	source, _ := value["source"].(string)
	if (gameKind == "hash" && source != "tron_hash") || (gameKind == "kline" && source != "okx_kline") {
		return nil, ErrInvalidGameType
	}
	if _, ok := value["payout_divisor"]; !ok {
		value["payout_divisor"] = 100
	}
	return json.Marshal(value)
}

func (service *Service) ListRounds(ctx context.Context, gameTypeCode, status string, limit int) ([]ManagedRound, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `
		SELECT rounds.id::text,rounds.game_type_id::text,game_types.code,rounds.sequence,
			rounds.status,rounds.bet_closes_at,rounds.result_at,rounds.outcome,rounds.settled_at
		FROM rounds JOIN game_types ON game_types.id=rounds.game_type_id
		WHERE ($1='' OR game_types.code=$1) AND ($2='' OR rounds.status=$2)
		ORDER BY rounds.bet_closes_at DESC LIMIT $3`, gameTypeCode, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ManagedRound, 0)
	for rows.Next() {
		var item ManagedRound
		if err := rows.Scan(&item.ID, &item.GameTypeID, &item.GameTypeCode, &item.Sequence, &item.Status, &item.BetClosesAt, &item.ResultAt, &item.Outcome, &item.SettledAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) CreateRound(ctx context.Context, gameTypeID string, betClosesAt time.Time) (ManagedRound, error) {
	if gameTypeID == "" || !betClosesAt.After(time.Now().UTC()) {
		return ManagedRound{}, ErrInvalidRound
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ManagedRound{}, err
	}
	defer tx.Rollback(ctx)
	var code string
	if err := tx.QueryRow(ctx, `SELECT code FROM game_types WHERE id=$1 AND enabled=true FOR UPDATE`, gameTypeID).Scan(&code); errors.Is(err, pgx.ErrNoRows) {
		return ManagedRound{}, ErrGameTypeNotFound
	} else if err != nil {
		return ManagedRound{}, err
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM rounds WHERE game_type_id=$1`, gameTypeID).Scan(&sequence); err != nil {
		return ManagedRound{}, err
	}
	var item ManagedRound
	err = tx.QueryRow(ctx, `
		INSERT INTO rounds (id,game_type_id,sequence,status,bet_closes_at)
		VALUES ($1,$2,$3,'open',$4)
		RETURNING id::text,game_type_id::text,sequence,status,bet_closes_at,result_at`,
		uuid.NewString(), gameTypeID, sequence, betClosesAt.UTC()).
		Scan(&item.ID, &item.GameTypeID, &item.Sequence, &item.Status, &item.BetClosesAt, &item.ResultAt)
	if err != nil {
		return ManagedRound{}, err
	}
	item.GameTypeCode = code
	return item, tx.Commit(ctx)
}

func validateGameType(input GameTypeInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return ErrInvalidGameType
	}
	if _, err := game.ParseRules(input.Rules); err != nil {
		return ErrInvalidGameType
	}
	if input.BlockInterval < 0 {
		return ErrInvalidGameType
	}
	if seconds := normalizeGameCloseBeforeSecs(input.CloseBeforeSecs); seconds < 1 || seconds > 59 {
		return ErrInvalidGameType
	}
	return nil
}

func normalizeGameCloseBeforeSecs(value int) int {
	if value == 0 {
		return 3
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
