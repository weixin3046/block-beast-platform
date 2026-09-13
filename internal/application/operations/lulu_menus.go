package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrInvalidLuluPlayConfig = errors.New("invalid lulu room play configuration")
var ErrLuluPlayConfigNotFound = errors.New("lulu room play configuration not found")

// LuluMenus is the player-facing configuration for the three shared Lulu games.
// Amounts are stored in minor units; clients use /v1/currencies for decimals.
type LuluMenus struct {
	ServerTime time.Time      `json:"server_time"`
	Games      []LuluMenuGame `json:"games"`
}

type LuluMenuGame struct {
	Code  string         `json:"code"`
	Name  string         `json:"name"`
	Rooms []LuluMenuRoom `json:"rooms"`
}

type LuluMenuRoom struct {
	ID        string         `json:"id"`
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	SortOrder int            `json:"sort_order"`
	Plays     []LuluMenuPlay `json:"plays"`
}

type LuluMenuPlay struct {
	Code            string                   `json:"code"`
	Name            string                   `json:"name"`
	Outcomes        json.RawMessage          `json:"outcomes"`
	DodgeMode       bool                     `json:"dodge_mode"`
	CurrencyConfigs []LuluPlayCurrencyConfig `json:"currency_configs"`
}

type LuluPlayCurrencyConfig struct {
	Currency         string `json:"currency"`
	PayoutMultiplier int64  `json:"payout_multiplier"`
	PayoutDivisor    int64  `json:"payout_divisor"`
	MinStakeMinor    int64  `json:"min_stake_minor"`
	MaxStakeMinor    int64  `json:"max_stake_minor"`
}

type LuluRoomPlayConfigUpdate struct {
	SecondPassword  string                   `json:"second_password,omitempty"`
	GameType        string                   `json:"game_type"`
	RoomID          string                   `json:"room_id"`
	PlayCode        string                   `json:"play_code"`
	CurrencyConfigs []LuluPlayCurrencyConfig `json:"currency_configs"`
}

type LuluRoomPlayConfigsUpdate struct {
	SecondPassword string                     `json:"second_password,omitempty"`
	Configs        []LuluRoomPlayConfigUpdate `json:"configs"`
}

func (service *Service) UpdateLuluRoomPlayConfig(ctx context.Context, input LuluRoomPlayConfigUpdate) (LuluMenus, error) {
	return service.UpdateLuluRoomPlayConfigs(ctx, []LuluRoomPlayConfigUpdate{input})
}

func (service *Service) UpdateLuluRoomPlayConfigs(ctx context.Context, inputs []LuluRoomPlayConfigUpdate) (LuluMenus, error) {
	if len(inputs) == 0 {
		return LuluMenus{}, ErrInvalidLuluPlayConfig
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return LuluMenus{}, err
	}
	defer tx.Rollback(ctx)
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		key := strings.TrimSpace(input.GameType) + ":" + strings.TrimSpace(input.RoomID) + ":" + strings.TrimSpace(input.PlayCode)
		if _, ok := seen[key]; ok {
			return LuluMenus{}, ErrInvalidLuluPlayConfig
		}
		seen[key] = struct{}{}
		if err := updateLuluRoomPlayConfigTx(ctx, tx, input); err != nil {
			return LuluMenus{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return LuluMenus{}, err
	}
	return service.GetLuluMenus(ctx)
}

func updateLuluRoomPlayConfigTx(ctx context.Context, tx pgx.Tx, input LuluRoomPlayConfigUpdate) error {
	input.GameType = strings.TrimSpace(input.GameType)
	input.RoomID = strings.TrimSpace(input.RoomID)
	input.PlayCode = strings.TrimSpace(input.PlayCode)
	if input.GameType == "" || input.RoomID == "" || input.PlayCode == "" || len(input.CurrencyConfigs) == 0 {
		return ErrInvalidLuluPlayConfig
	}
	seen := make(map[string]struct{}, len(input.CurrencyConfigs))
	for i := range input.CurrencyConfigs {
		item := &input.CurrencyConfigs[i]
		item.Currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		if item.Currency == "" || item.PayoutMultiplier <= 0 || item.PayoutDivisor <= 0 || item.MinStakeMinor <= 0 || item.MaxStakeMinor < item.MinStakeMinor {
			return ErrInvalidLuluPlayConfig
		}
		if _, ok := seen[item.Currency]; ok {
			return ErrInvalidLuluPlayConfig
		}
		seen[item.Currency] = struct{}{}
	}
	var gameID string
	err := tx.QueryRow(ctx, `SELECT gt.id::text FROM game_types gt
		JOIN game_room_types rt ON rt.game_type_id=gt.id
		JOIN lulu_play_configs p ON p.game_type_id=gt.id AND p.code=$3 AND p.enabled=true
		WHERE gt.code=$1 AND rt.room_id=$2 AND gt.enabled=true
		AND gt.rules->>'source'='lulu_ws' AND gt.rules->'extras'->>'lulu_shared'='true'`, input.GameType, input.RoomID, input.PlayCode).Scan(&gameID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLuluPlayConfigNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM lulu_room_play_currency_configs WHERE game_type_id=$1 AND room_id=$2 AND play_code=$3`, gameID, input.RoomID, input.PlayCode); err != nil {
		return err
	}
	for _, item := range input.CurrencyConfigs {
		if _, err = tx.Exec(ctx, `INSERT INTO lulu_room_play_currency_configs(game_type_id,room_id,play_code,currency,payout_multiplier,payout_divisor,min_stake_minor,max_stake_minor)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, gameID, input.RoomID, input.PlayCode, item.Currency, item.PayoutMultiplier, item.PayoutDivisor, item.MinStakeMinor, item.MaxStakeMinor); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) GetLuluMenus(ctx context.Context) (LuluMenus, error) {
	result := LuluMenus{ServerTime: time.Now().UTC(), Games: make([]LuluMenuGame, 0, 3)}
	games, err := service.pool.Query(ctx, `
		SELECT id::text,code,name FROM game_types
		WHERE enabled=true AND rules->>'source'='lulu_ws'
			AND rules->'extras'->>'lulu_shared'='true'
		ORDER BY code`)
	if err != nil {
		return LuluMenus{}, err
	}
	defer games.Close()
	type gameRow struct {
		id   string
		game LuluMenuGame
	}
	items := make([]gameRow, 0, 3)
	for games.Next() {
		var game LuluMenuGame
		var gameID string
		if err := games.Scan(&gameID, &game.Code, &game.Name); err != nil {
			return LuluMenus{}, err
		}
		items = append(items, gameRow{id: gameID, game: game})
	}
	if err := games.Err(); err != nil {
		return LuluMenus{}, err
	}
	games.Close()
	for _, item := range items {
		item.game.Rooms, err = service.luluMenuRooms(ctx, item.id)
		if err != nil {
			return LuluMenus{}, err
		}
		result.Games = append(result.Games, item.game)
	}
	return result, nil
}

func (service *Service) luluMenuRooms(ctx context.Context, gameID string) ([]LuluMenuRoom, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT r.id::text,r.code,r.name,r.sort_order
		FROM game_room_types rt JOIN game_rooms r ON r.id=rt.room_id
		WHERE rt.game_type_id=$1 AND r.enabled=true ORDER BY r.sort_order,r.id`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]LuluMenuRoom, 0, 6)
	type roomRow struct{ room LuluMenuRoom }
	items := make([]roomRow, 0, 6)
	for rows.Next() {
		var room LuluMenuRoom
		if err := rows.Scan(&room.ID, &room.Code, &room.Name, &room.SortOrder); err != nil {
			return nil, err
		}
		items = append(items, roomRow{room: room})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, item := range items {
		plays, err := service.luluMenuPlays(ctx, gameID, item.room.ID)
		if err != nil {
			return nil, err
		}
		item.room.Plays = plays
		rooms = append(rooms, item.room)
	}
	return rooms, nil
}

func (service *Service) luluMenuPlays(ctx context.Context, gameID, roomID string) ([]LuluMenuPlay, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT p.code,p.name,p.outcomes,p.dodge_mode,c.currency,c.payout_multiplier,c.payout_divisor,c.min_stake_minor,c.max_stake_minor
		FROM lulu_play_configs p
		JOIN lulu_room_play_currency_configs c ON c.game_type_id=p.game_type_id AND c.play_code=p.code
		WHERE p.game_type_id=$1 AND c.room_id=$2 AND p.enabled=true
		ORDER BY p.sort_order,p.code,c.currency`, gameID, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plays := make([]LuluMenuPlay, 0)
	index := make(map[string]int)
	for rows.Next() {
		var code string
		var config LuluPlayCurrencyConfig
		var outcomes json.RawMessage
		var name string
		var dodge bool
		if err := rows.Scan(&code, &name, &outcomes, &dodge, &config.Currency, &config.PayoutMultiplier, &config.PayoutDivisor, &config.MinStakeMinor, &config.MaxStakeMinor); err != nil {
			return nil, err
		}
		position, ok := index[code]
		if !ok {
			position = len(plays)
			index[code] = position
			plays = append(plays, LuluMenuPlay{Code: code, Name: name, Outcomes: outcomes, DodgeMode: dodge, CurrencyConfigs: make([]LuluPlayCurrencyConfig, 0)})
		}
		plays[position].CurrencyConfigs = append(plays[position].CurrencyConfigs, config)
	}
	return plays, rows.Err()
}
