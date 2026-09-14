package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/block-beast/platform/internal/domain/game"
)

// DueSettlement 描述一次到期结算的执行结果。
type DueSettlement struct {
	RoundID string
	Result  SettlementResult
}

// SettleDueRounds 批量结算处于 closed（或中断在 settling）状态的轮次：
// 按玩法加载规则、从结果来源开奖，然后独立事务地逐轮结算。
// 单个轮次失败会回滚并保持原状态，等待下个调度周期重试，不影响其他轮次。
func (service *Service) SettleDueRounds(ctx context.Context, source ResultSource, limit int) ([]DueSettlement, error) {
	if limit <= 0 {
		return []DueSettlement{}, nil
	}
	rows, err := service.pool.Query(ctx, `
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at, rounds.result_at, game_types.rules
		FROM rounds
		JOIN game_types ON game_types.id = rounds.game_type_id
		WHERE rounds.status IN ('closed', 'settling') AND rounds.result_at <= now()
		-- Lulu issues without a confirmed upstream result remain retryable, but
		-- must not occupy the batch ahead of newer confirmed issues. Otherwise a
		-- historical gap can indefinitely hide every subsequent result from the
		-- game page and block its settlement.
		ORDER BY CASE
			WHEN game_types.rules->>'source'='lulu_ws' AND EXISTS (
				SELECT 1 FROM external_draw_rounds draw
				WHERE draw.source='lulu_ws'
					AND draw.game=game_types.rules->'extras'->>'external_game'
					AND draw.external_round=rounds.sequence
					AND draw.status='confirmed'
			) THEN 0
			WHEN game_types.rules->>'source'='tron_hash' THEN 1
			ELSE 2
		END,
			rounds.result_at, rounds.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	type dueRound struct {
		round game.Round
		rules json.RawMessage
	}
	pending := make([]dueRound, 0)
	for rows.Next() {
		var item dueRound
		if err := rows.Scan(&item.round.RoundID, &item.round.GameType, &item.round.Sequence, &item.round.Status, &item.round.BetClosesAt, &item.round.ResultAt, &item.rules); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	settled := make([]DueSettlement, 0, len(pending))
	failures := make([]error, 0)
	for _, item := range pending {
		rules, err := game.ParseRules(item.rules)
		if err != nil {
			failures = append(failures, fmt.Errorf("round %s: %w", item.round.RoundID, err))
			continue
		}
		outcome, err := source.Outcome(ctx, item.round, rules)
		if err != nil {
			// 目标区块尚未产生是正常状态，下个高频周期立即重试，不记录为失败。
			if errors.Is(err, ErrBlockNotFound) {
				continue
			}
			failures = append(failures, fmt.Errorf("round %s outcome: %w", item.round.RoundID, err))
			// A shared upstream rate limit applies to every remaining hash round in
			// this batch. Stop immediately instead of multiplying calls and making
			// recovery take longer.
			if errors.Is(err, ErrRateLimited) {
				break
			}
			continue
		}
		result, err := service.SettleRound(ctx, item.round.RoundID, outcome, rules)
		if err != nil {
			failures = append(failures, fmt.Errorf("round %s settle: %w", item.round.RoundID, err))
			continue
		}
		settled = append(settled, DueSettlement{RoundID: item.round.RoundID, Result: result})
	}
	return settled, errors.Join(failures...)
}
