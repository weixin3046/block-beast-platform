package virtualbot

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
	bets *betting.Service
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, bets *betting.Service) *Service {
	return &Service{pool: pool, bets: bets, now: time.Now}
}

func (s *Service) RunDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT a.user_id::text,a.currency,a.stake_minor,a.game_type_codes FROM virtual_account_automations a JOIN users u ON u.id=a.user_id WHERE u.is_virtual=true AND u.status='active' AND a.enabled=true AND (a.last_run_at IS NULL OR a.last_run_at + make_interval(secs=>a.interval_seconds) <= now()) ORDER BY a.updated_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, currency string
		stake        int64
		codes        []string
	}
	items := []due{}
	for rows.Next() {
		var v due
		if err := rows.Scan(&v.id, &v.currency, &v.stake, &v.codes); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	placed := 0
	for _, item := range items {
		for _, code := range item.codes {
			var roundID string
			var raw json.RawMessage
			err := s.pool.QueryRow(ctx, `SELECT r.id::text,gt.rules FROM rounds r JOIN game_types gt ON gt.id=r.game_type_id WHERE gt.code=$1 AND gt.enabled=true AND r.status='open' AND r.bet_closes_at>now() ORDER BY r.sequence LIMIT 1`, code).Scan(&roundID, &raw)
			if err != nil {
				continue
			}
			rules, err := game.ParseRules(raw)
			if err != nil || len(rules.Outcomes) == 0 {
				continue
			}
			value := rules.Outcomes[int(s.now().UnixNano()%int64(len(rules.Outcomes)))]
			selection := selectionJSON(rules.MatchField, value)
			_, err = s.bets.PlaceBet(ctx, betting.PlaceBetRequest{ClientRequestID: "virtual-" + uuid.NewString(), RoundID: roundID, AccountID: item.id, Currency: item.currency, Selection: selection, StakeMinor: item.stake})
			if err == nil {
				placed++
			}
		}
		_, _ = s.pool.Exec(ctx, `UPDATE virtual_account_automations SET last_run_at=now() WHERE user_id=$1`, item.id)
	}
	return placed, nil
}

func selectionJSON(field, value string) json.RawMessage {
	if strings.TrimSpace(field) == "" {
		field = "pick"
	}
	parts := strings.Split(field, ".")
	var node any = value
	for i := len(parts) - 1; i >= 0; i-- {
		node = map[string]any{parts[i]: node}
	}
	raw, _ := json.Marshal(node)
	return raw
}
