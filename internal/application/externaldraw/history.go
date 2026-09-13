package externaldraw

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	GameStarSea      = "star_sea"
	GameAngryFeather = "angry_feather"
	GameGreenSprint  = "green_sprint"
)

var ErrUnknownGame = errors.New("unknown external draw game")

type Record struct {
	Issue string `json:"issue"`
	Room  []int  `json:"room"`
}

type HistoryReader struct{ pool *pgxpool.Pool }

func NewHistoryReader(pool *pgxpool.Pool) *HistoryReader { return &HistoryReader{pool: pool} }

func (reader *HistoryReader) History(ctx context.Context, game string, count int) ([]Record, error) {
	sourceGame, ok := sourceGame(game)
	if !ok {
		return nil, ErrUnknownGame
	}
	if reader == nil || reader.pool == nil || count < 1 {
		return nil, ErrInvalidEvent
	}
	rows, err := reader.pool.Query(ctx, `SELECT external_round,outcome FROM external_draw_rounds
		WHERE source='lulu_ws' AND game=$1 AND status='confirmed'
		ORDER BY external_round DESC LIMIT $2`, sourceGame, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Record, 0)
	for rows.Next() {
		var sequence int64
		var raw json.RawMessage
		if err = rows.Scan(&sequence, &raw); err != nil {
			return nil, err
		}
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
			continue
		}
		rooms := make([]int, 0, len(values))
		for _, value := range values {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < 1 {
				rooms = nil
				break
			}
			rooms = append(rooms, parsed)
		}
		if len(rooms) > 0 {
			items = append(items, Record{Issue: strconv.FormatInt(sequence, 10), Room: rooms})
		}
	}
	return items, rows.Err()
}

func sourceGame(game string) (string, bool) {
	switch game {
	case GameStarSea:
		return "xdy", true
	case GameAngryFeather:
		return "lh", true
	case GameGreenSprint:
		return "race", true
	default:
		return "", false
	}
}
