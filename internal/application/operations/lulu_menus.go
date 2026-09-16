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

// LuluPrimeTimeConfig is one prime-time (Beijing 20:00-21:00) odds override
// for 星海逃杀. Rows with enabled=false keep the daily configuration.
type LuluPrimeTimeConfig struct {
	Currency         string `json:"currency"`
	PayoutMultiplier int64  `json:"payout_multiplier"`
	PayoutDivisor    int64  `json:"payout_divisor"`
	MinStakeMinor    int64  `json:"min_stake_minor"`
	MaxStakeMinor    int64  `json:"max_stake_minor"`
	Enabled          bool   `json:"enabled"`
}

type LuluPrimeTimeConfigUpdate struct {
	SecondPassword  string                `json:"second_password,omitempty"`
	GameType        string                `json:"game_type"`
	RoomID          string                `json:"room_id"`
	PlayCode        string                `json:"play_code"`
	CurrencyConfigs []LuluPrimeTimeConfig `json:"currency_configs"`
}

type LuluPrimeTimeConfigsUpdate struct {
	SecondPassword string                      `json:"second_password,omitempty"`
	Configs        []LuluPrimeTimeConfigUpdate `json:"configs"`
}

var ErrLuluPrimeTimeConfigInvalid = errors.New("invalid lulu prime time configuration")
var ErrLuluPrimeTimeConfigNotFound = errors.New("lulu prime time configuration not found")

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

func (service *Service) UpdateLuluPrimeTimeConfig(ctx context.Context, input LuluPrimeTimeConfigUpdate) ([]LuluPrimeTimeConfigUpdate, error) {
	return service.UpdateLuluPrimeTimeConfigs(ctx, []LuluPrimeTimeConfigUpdate{input})
}

// UpdateLuluPrimeTimeConfigs stores prime-time (Beijing 20:00-21:00) odds
// overrides for 星海逃杀. All entries commit in one transaction. Pass an empty
// currency_configs list to delete the override so the play falls back to the
// daily configuration.
func (service *Service) UpdateLuluPrimeTimeConfigs(ctx context.Context, inputs []LuluPrimeTimeConfigUpdate) ([]LuluPrimeTimeConfigUpdate, error) {
	if len(inputs) == 0 {
		return nil, ErrLuluPrimeTimeConfigInvalid
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		key := strings.TrimSpace(input.GameType) + ":" + strings.TrimSpace(input.RoomID) + ":" + strings.TrimSpace(input.PlayCode)
		if _, ok := seen[key]; ok {
			return nil, ErrLuluPrimeTimeConfigInvalid
		}
		seen[key] = struct{}{}
		if err := updateLuluPrimeTimeConfigTx(ctx, tx, input); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	result := make([]LuluPrimeTimeConfigUpdate, 0, len(inputs))
	for _, input := range inputs {
		input.SecondPassword = ""
		result = append(result, input)
	}
	return result, nil
}

func updateLuluPrimeTimeConfigTx(ctx context.Context, tx pgx.Tx, input LuluPrimeTimeConfigUpdate) error {
	input.GameType = strings.TrimSpace(input.GameType)
	input.RoomID = strings.TrimSpace(input.RoomID)
	input.PlayCode = strings.TrimSpace(input.PlayCode)
	if input.GameType == "" || input.RoomID == "" || input.PlayCode == "" {
		return ErrLuluPrimeTimeConfigInvalid
	}
	if input.PlayCode != "odd_even" && input.PlayCode != "dodge" {
		return ErrLuluPrimeTimeConfigInvalid
	}
	seen := make(map[string]struct{}, len(input.CurrencyConfigs))
	for i := range input.CurrencyConfigs {
		item := &input.CurrencyConfigs[i]
		item.Currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		if item.Currency == "" || item.PayoutMultiplier <= 0 || item.PayoutDivisor <= 0 || item.MinStakeMinor <= 0 || item.MaxStakeMinor < item.MinStakeMinor {
			return ErrLuluPrimeTimeConfigInvalid
		}
		if _, ok := seen[item.Currency]; ok {
			return ErrLuluPrimeTimeConfigInvalid
		}
		seen[item.Currency] = struct{}{}
	}
	var gameID string
	err := tx.QueryRow(ctx, `SELECT gt.id::text FROM game_types gt
		JOIN game_room_types rt ON rt.game_type_id=gt.id
		JOIN lulu_play_configs p ON p.game_type_id=gt.id AND p.code=$3 AND p.enabled=true
		WHERE gt.code=$1 AND rt.room_id=$2 AND gt.code='lulu-xdy' AND gt.enabled=true
		AND gt.rules->>'source'='lulu_ws' AND gt.rules->'extras'->>'lulu_shared'='true'`, input.GameType, input.RoomID, input.PlayCode).Scan(&gameID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLuluPrimeTimeConfigNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM lulu_prime_time_configs WHERE game_type_id=$1 AND room_id=$2 AND play_code=$3`, gameID, input.RoomID, input.PlayCode); err != nil {
		return err
	}
	for _, item := range input.CurrencyConfigs {
		if _, err = tx.Exec(ctx, `INSERT INTO lulu_prime_time_configs(game_type_id,room_id,play_code,currency,payout_multiplier,payout_divisor,min_stake_minor,max_stake_minor,enabled)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, gameID, input.RoomID, input.PlayCode, item.Currency, item.PayoutMultiplier, item.PayoutDivisor, item.MinStakeMinor, item.MaxStakeMinor, item.Enabled); err != nil {
			return err
		}
	}
	return nil
}

// GetLuluPrimeTimeConfigs returns every stored prime-time override keyed by
// game type, room, and play code. Enabled=false rows are included so the
// back office can display and re-enable them.
func (service *Service) GetLuluPrimeTimeConfigs(ctx context.Context) ([]LuluPrimeTimeConfigUpdate, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT gt.code,r.id::text,c.play_code,c.currency,c.payout_multiplier,c.payout_divisor,c.min_stake_minor,c.max_stake_minor,c.enabled
		FROM lulu_prime_time_configs c
		JOIN game_types gt ON gt.id=c.game_type_id
		JOIN game_rooms r ON r.id=c.room_id
		WHERE gt.code='lulu-xdy'
		ORDER BY gt.code,r.sort_order,r.id,c.play_code,c.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := map[string]int{}
	result := make([]LuluPrimeTimeConfigUpdate, 0)
	for rows.Next() {
		var gameType, roomID, playCode, currency string
		var multiplier, divisor, min, max int64
		var enabled bool
		if err := rows.Scan(&gameType, &roomID, &playCode, &currency, &multiplier, &divisor, &min, &max, &enabled); err != nil {
			return nil, err
		}
		key := gameType + "\x00" + roomID + "\x00" + playCode
		position, ok := index[key]
		if !ok {
			position = len(result)
			index[key] = position
			result = append(result, LuluPrimeTimeConfigUpdate{GameType: gameType, RoomID: roomID, PlayCode: playCode, CurrencyConfigs: make([]LuluPrimeTimeConfig, 0)})
		}
		result[position].CurrencyConfigs = append(result[position].CurrencyConfigs, LuluPrimeTimeConfig{
			Currency: currency, PayoutMultiplier: multiplier, PayoutDivisor: divisor,
			MinStakeMinor: min, MaxStakeMinor: max, Enabled: enabled,
		})
	}
	return result, rows.Err()
}

func (service *Service) GetLuluMenus(ctx context.Context) (LuluMenus, error) {
	result := LuluMenus{ServerTime: time.Now().UTC(), Games: make([]LuluMenuGame, 0, 3)}
	rows, err := service.pool.Query(ctx, `
		SELECT gt.code,gt.name,r.id::text,r.code,r.name,r.sort_order,
			p.code,p.name,p.outcomes,p.dodge_mode,
			c.currency,c.payout_multiplier,c.payout_divisor,c.min_stake_minor,c.max_stake_minor
		FROM game_types gt
		JOIN game_room_types rt ON rt.game_type_id=gt.id
		JOIN game_rooms r ON r.id=rt.room_id AND r.enabled=true
		JOIN lulu_play_configs p ON p.game_type_id=gt.id AND p.enabled=true
		JOIN lulu_room_play_currency_configs c ON c.game_type_id=gt.id AND c.room_id=r.id AND c.play_code=p.code
		WHERE gt.enabled=true AND gt.rules->>'source'='lulu_ws'
			AND gt.rules->'extras'->>'lulu_shared'='true'
		ORDER BY gt.code,r.sort_order,r.id,p.sort_order,p.code,c.currency`)
	if err != nil {
		return LuluMenus{}, err
	}
	defer rows.Close()
	items := make([]luluMenuRow, 0, 64)
	for rows.Next() {
		var item luluMenuRow
		if err := rows.Scan(&item.gameCode, &item.gameName, &item.roomID, &item.roomCode, &item.roomName, &item.roomSort,
			&item.playCode, &item.playName, &item.outcomes, &item.dodge,
			&item.currency, &item.multiplier, &item.divisor, &item.min, &item.max); err != nil {
			return LuluMenus{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return LuluMenus{}, err
	}
	result.Games = assembleLuluMenus(items).Games
	return result, nil
}

type luluMenuRow struct {
	gameCode, gameName, roomID, roomCode, roomName, playCode, playName, currency string
	roomSort                                                                     int
	outcomes                                                                     json.RawMessage
	dodge                                                                        bool
	multiplier, divisor, min, max                                                int64
}

func assembleLuluMenus(rows []luluMenuRow) LuluMenus {
	result := LuluMenus{Games: make([]LuluMenuGame, 0, 3)}
	gameIndex := map[string]int{}
	roomIndex := map[string]int{}
	playIndex := map[string]int{}
	for _, row := range rows {
		gamePos, ok := gameIndex[row.gameCode]
		if !ok {
			gamePos = len(result.Games)
			gameIndex[row.gameCode] = gamePos
			result.Games = append(result.Games, LuluMenuGame{Code: row.gameCode, Name: row.gameName, Rooms: make([]LuluMenuRoom, 0, 6)})
		}
		roomKey := row.gameCode + "\x00" + row.roomID
		roomPos, ok := roomIndex[roomKey]
		if !ok {
			roomPos = len(result.Games[gamePos].Rooms)
			roomIndex[roomKey] = roomPos
			result.Games[gamePos].Rooms = append(result.Games[gamePos].Rooms, LuluMenuRoom{ID: row.roomID, Code: row.roomCode, Name: row.roomName, SortOrder: row.roomSort, Plays: make([]LuluMenuPlay, 0)})
		}
		playKey := roomKey + "\x00" + row.playCode
		playPos, ok := playIndex[playKey]
		if !ok {
			playPos = len(result.Games[gamePos].Rooms[roomPos].Plays)
			playIndex[playKey] = playPos
			result.Games[gamePos].Rooms[roomPos].Plays = append(result.Games[gamePos].Rooms[roomPos].Plays, LuluMenuPlay{Code: row.playCode, Name: row.playName, Outcomes: row.outcomes, DodgeMode: row.dodge, CurrencyConfigs: make([]LuluPlayCurrencyConfig, 0)})
		}
		play := &result.Games[gamePos].Rooms[roomPos].Plays[playPos]
		play.CurrencyConfigs = append(play.CurrencyConfigs, LuluPlayCurrencyConfig{Currency: row.currency, PayoutMultiplier: row.multiplier, PayoutDivisor: row.divisor, MinStakeMinor: row.min, MaxStakeMinor: row.max})
	}
	return result
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
