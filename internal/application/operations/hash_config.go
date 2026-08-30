package operations

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrInvalidHashConfig = errors.New("invalid hash game configuration")
var ErrHashConfigConflict = errors.New("hash game configuration version conflict")

type HashCurrencyConfig struct {
	Currency           string `json:"currency"`
	GuessMultiplier    int64  `json:"guess_multiplier"`
	GuessDivisor       int64  `json:"guess_divisor"`
	DodgeMultiplier    int64  `json:"dodge_multiplier"`
	DodgeDivisor       int64  `json:"dodge_divisor"`
	RoadMultiplier     int64  `json:"road_multiplier"`
	RoadDivisor        int64  `json:"road_divisor"`
	GuessMaxStakeMinor int64  `json:"guess_max_stake_minor"`
	DodgeMaxStakeMinor int64  `json:"dodge_max_stake_minor"`
	RoadMaxStakeMinor  int64  `json:"road_max_stake_minor"`
	MinStakeMinor      int64  `json:"min_stake_minor"`
	WaterMultiplier    int64  `json:"water_multiplier"`
	WaterDivisor       int64  `json:"water_divisor"`
}

type HashRoomConfig struct {
	ID              string               `json:"id"`
	Code            string               `json:"code"`
	Name            string               `json:"name"`
	Enabled         bool                 `json:"enabled"`
	SortOrder       int                  `json:"sort_order"`
	GameTypes       []GameType           `json:"game_types"`
	CurrencyConfigs []HashCurrencyConfig `json:"currency_configs"`
}

type HashConfig struct {
	Version    int64            `json:"version"`
	ServerTime time.Time        `json:"server_time"`
	Rooms      []HashRoomConfig `json:"rooms"`
}

type HashConfigUpdate struct {
	ExpectedVersion int64            `json:"expected_version"`
	Rooms           []HashRoomConfig `json:"rooms"`
}

func (service *Service) GetHashConfig(ctx context.Context, enabledOnly bool) (HashConfig, error) {
	var result HashConfig
	if err := service.pool.QueryRow(ctx, `SELECT version FROM hash_game_settings WHERE singleton=true`).Scan(&result.Version); err != nil {
		return HashConfig{}, err
	}
	result.ServerTime = time.Now().UTC()
	rooms, err := service.ListGameRooms(ctx, enabledOnly)
	if err != nil {
		return HashConfig{}, err
	}
	result.Rooms = make([]HashRoomConfig, 0, 6)
	for _, room := range rooms {
		if room.GameKind != "hash" {
			continue
		}
		item := HashRoomConfig{ID: room.ID, Code: room.Code, Name: room.Name, Enabled: room.Enabled, SortOrder: room.SortOrder, GameTypes: room.GameTypes, CurrencyConfigs: make([]HashCurrencyConfig, 0)}
		rows, err := service.pool.Query(ctx, `
			SELECT currency,guess_multiplier,guess_divisor,dodge_multiplier,dodge_divisor,
				road_multiplier,road_divisor,guess_max_stake_minor,dodge_max_stake_minor,
				road_max_stake_minor,min_stake_minor,water_multiplier,water_divisor
			FROM hash_room_currency_configs WHERE room_id=$1 ORDER BY currency`, room.ID)
		if err != nil {
			return HashConfig{}, err
		}
		for rows.Next() {
			var config HashCurrencyConfig
			if err := rows.Scan(&config.Currency, &config.GuessMultiplier, &config.GuessDivisor,
				&config.DodgeMultiplier, &config.DodgeDivisor, &config.RoadMultiplier,
				&config.RoadDivisor, &config.GuessMaxStakeMinor, &config.DodgeMaxStakeMinor,
				&config.RoadMaxStakeMinor, &config.MinStakeMinor, &config.WaterMultiplier,
				&config.WaterDivisor); err != nil {
				rows.Close()
				return HashConfig{}, err
			}
			item.CurrencyConfigs = append(item.CurrencyConfigs, config)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return HashConfig{}, err
		}
		rows.Close()
		result.Rooms = append(result.Rooms, item)
	}
	return result, nil
}

func (service *Service) UpdateHashConfig(ctx context.Context, input HashConfigUpdate) (HashConfig, error) {
	if input.ExpectedVersion <= 0 || len(input.Rooms) != 6 {
		return HashConfig{}, ErrInvalidHashConfig
	}
	seenRooms := make(map[string]struct{}, len(input.Rooms))
	for _, room := range input.Rooms {
		if room.ID == "" || strings.TrimSpace(room.Name) == "" || len(room.CurrencyConfigs) == 0 {
			return HashConfig{}, ErrInvalidHashConfig
		}
		if _, exists := seenRooms[room.ID]; exists {
			return HashConfig{}, ErrInvalidHashConfig
		}
		seenRooms[room.ID] = struct{}{}
		seenCurrencies := make(map[string]struct{}, len(room.CurrencyConfigs))
		for _, config := range room.CurrencyConfigs {
			currency := strings.ToUpper(strings.TrimSpace(config.Currency))
			if currency == "" || config.GuessMultiplier <= 0 || config.GuessDivisor <= 0 ||
				config.DodgeMultiplier <= 0 || config.DodgeDivisor <= 0 || config.RoadMultiplier <= 0 ||
				config.RoadDivisor <= 0 || config.GuessMaxStakeMinor <= 0 || config.DodgeMaxStakeMinor <= 0 ||
				config.RoadMaxStakeMinor <= 0 || config.MinStakeMinor <= 0 || config.WaterMultiplier < 0 ||
				config.WaterDivisor <= 0 {
				return HashConfig{}, ErrInvalidHashConfig
			}
			if _, exists := seenCurrencies[currency]; exists {
				return HashConfig{}, ErrInvalidHashConfig
			}
			seenCurrencies[currency] = struct{}{}
		}
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return HashConfig{}, err
	}
	defer tx.Rollback(ctx)
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM hash_game_settings WHERE singleton=true FOR UPDATE`).Scan(&version); err != nil {
		return HashConfig{}, err
	}
	if version != input.ExpectedVersion {
		return HashConfig{}, ErrHashConfigConflict
	}
	for _, room := range input.Rooms {
		command, err := tx.Exec(ctx, `UPDATE game_rooms SET name=$2,enabled=$3,sort_order=$4,updated_at=now() WHERE id=$1 AND game_kind='hash'`, room.ID, strings.TrimSpace(room.Name), room.Enabled, room.SortOrder)
		if err != nil {
			return HashConfig{}, err
		}
		if command.RowsAffected() != 1 {
			return HashConfig{}, ErrInvalidHashConfig
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hash_room_currency_configs WHERE room_id=$1`, room.ID); err != nil {
			return HashConfig{}, err
		}
		for _, config := range room.CurrencyConfigs {
			_, err := tx.Exec(ctx, `INSERT INTO hash_room_currency_configs(
				room_id,currency,guess_multiplier,guess_divisor,dodge_multiplier,dodge_divisor,
				road_multiplier,road_divisor,guess_max_stake_minor,dodge_max_stake_minor,
				road_max_stake_minor,min_stake_minor,water_multiplier,water_divisor)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				room.ID, strings.ToUpper(strings.TrimSpace(config.Currency)), config.GuessMultiplier,
				config.GuessDivisor, config.DodgeMultiplier, config.DodgeDivisor,
				config.RoadMultiplier, config.RoadDivisor, config.GuessMaxStakeMinor,
				config.DodgeMaxStakeMinor, config.RoadMaxStakeMinor, config.MinStakeMinor,
				config.WaterMultiplier, config.WaterDivisor)
			if err != nil {
				return HashConfig{}, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE hash_game_settings SET version=version+1,updated_at=now() WHERE singleton=true`); err != nil {
		return HashConfig{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return HashConfig{}, err
	}
	return service.GetHashConfig(ctx, false)
}
