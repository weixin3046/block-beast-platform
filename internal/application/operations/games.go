package operations

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInvalidGameType = errors.New("game code, name and valid rules are required")
var ErrGameTypeNotFound = errors.New("game type not found")
var ErrGameTypeConflict = errors.New("game code already exists")
var ErrInvalidRound = errors.New("game type and future bet close time are required")

var gameCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

type GameType struct {
	ID        string          `json:"id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Rules     json.RawMessage `json:"rules"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type GameTypeInput struct {
	Code    string          `json:"code"`
	Name    string          `json:"name"`
	Enabled bool            `json:"enabled"`
	Rules   json.RawMessage `json:"rules"`
}

type ManagedRound struct {
	ID           string          `json:"id"`
	GameTypeID   string          `json:"game_type_id"`
	GameTypeCode string          `json:"game_type_code"`
	Sequence     int64           `json:"sequence"`
	Status       string          `json:"status"`
	BetClosesAt  time.Time       `json:"bet_closes_at"`
	Outcome      json.RawMessage `json:"outcome,omitempty"`
	SettledAt    *time.Time      `json:"settled_at,omitempty"`
}

func (service *Service) ListGameTypes(ctx context.Context) ([]GameType, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text,code,name,enabled,rules,created_at,updated_at FROM game_types ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]GameType, 0)
	for rows.Next() {
		var item GameType
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) CreateGameType(ctx context.Context, input GameTypeInput) (GameType, error) {
	if err := validateGameType(input); err != nil {
		return GameType{}, err
	}
	var item GameType
	err := service.pool.QueryRow(ctx, `
		INSERT INTO game_types (id,code,name,enabled,rules)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id::text,code,name,enabled,rules,created_at,updated_at`,
		uuid.NewString(), strings.TrimSpace(input.Code), strings.TrimSpace(input.Name), input.Enabled, input.Rules).
		Scan(&item.ID, &item.Code, &item.Name, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt)
	if isUniqueViolation(err) {
		return GameType{}, ErrGameTypeConflict
	}
	return item, err
}

func (service *Service) UpdateGameType(ctx context.Context, id string, input GameTypeInput) (GameType, error) {
	if err := validateGameType(input); err != nil {
		return GameType{}, err
	}
	var item GameType
	err := service.pool.QueryRow(ctx, `
		UPDATE game_types SET code=$2,name=$3,enabled=$4,rules=$5,updated_at=now()
		WHERE id=$1
		RETURNING id::text,code,name,enabled,rules,created_at,updated_at`,
		id, strings.TrimSpace(input.Code), strings.TrimSpace(input.Name), input.Enabled, input.Rules).
		Scan(&item.ID, &item.Code, &item.Name, &item.Enabled, &item.Rules, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GameType{}, ErrGameTypeNotFound
	}
	if isUniqueViolation(err) {
		return GameType{}, ErrGameTypeConflict
	}
	return item, err
}

func (service *Service) ListRounds(ctx context.Context, gameTypeCode, status string, limit int) ([]ManagedRound, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `
		SELECT rounds.id::text,rounds.game_type_id::text,game_types.code,rounds.sequence,
			rounds.status,rounds.bet_closes_at,rounds.outcome,rounds.settled_at
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
		if err := rows.Scan(&item.ID, &item.GameTypeID, &item.GameTypeCode, &item.Sequence, &item.Status, &item.BetClosesAt, &item.Outcome, &item.SettledAt); err != nil {
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
		RETURNING id::text,game_type_id::text,sequence,status,bet_closes_at`,
		uuid.NewString(), gameTypeID, sequence, betClosesAt.UTC()).
		Scan(&item.ID, &item.GameTypeID, &item.Sequence, &item.Status, &item.BetClosesAt)
	if err != nil {
		return ManagedRound{}, err
	}
	item.GameTypeCode = code
	return item, tx.Commit(ctx)
}

func validateGameType(input GameTypeInput) error {
	if !gameCodePattern.MatchString(strings.TrimSpace(input.Code)) || strings.TrimSpace(input.Name) == "" {
		return ErrInvalidGameType
	}
	if _, err := game.ParseRules(input.Rules); err != nil {
		return ErrInvalidGameType
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
