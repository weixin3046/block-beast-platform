package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/block-beast/platform/internal/domain/game"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LuluResultSource reads only confirmed, normalized draw results written by externaldraw.
type LuluResultSource struct{ pool *pgxpool.Pool }

func NewLuluResultSource(pool *pgxpool.Pool) LuluResultSource { return LuluResultSource{pool: pool} }

func (source LuluResultSource) Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error) {
	if err := rules.Validate(); err != nil {
		return nil, err
	}
	var extras struct {
		ExternalGame string `json:"external_game"`
	}
	if rules.Source != "lulu_ws" || json.Unmarshal(rules.Extras, &extras) != nil || source.pool == nil {
		return nil, game.ErrInvalidRules
	}
	var raw json.RawMessage
	err := source.pool.QueryRow(ctx, `SELECT outcome FROM external_draw_rounds
		WHERE source='lulu_ws' AND game=$1 AND external_round=$2 AND status='confirmed'`, extras.ExternalGame, round.Sequence).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBlockNotFound
	}
	if err != nil {
		return nil, err
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
		return nil, fmt.Errorf("decode lulu outcome: %w", err)
	}
	return luluOutcomeForRules(rules, values)
}

func luluOutcomeForRules(rules game.Rules, values []string) ([]string, error) {
	var extras struct {
		LuluShared bool                `json:"lulu_shared"`
		ResultMap  map[string][]string `json:"result_map"`
	}
	if json.Unmarshal(rules.Extras, &extras) != nil {
		return nil, game.ErrInvalidRules
	}
	if extras.LuluShared {
		return append([]string(nil), values...), nil
	}
	seen := make(map[string]struct{})
	outcome := make([]string, 0, len(values))
	for _, value := range values {
		for _, mapped := range extras.ResultMap[value] {
			if _, exists := seen[mapped]; !exists {
				seen[mapped] = struct{}{}
				outcome = append(outcome, mapped)
			}
		}
	}
	if len(outcome) == 0 {
		return nil, game.ErrInvalidRules
	}
	return outcome, nil
}
