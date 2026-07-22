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
		SELECT rounds.id, game_types.code, rounds.sequence, rounds.status, rounds.bet_closes_at, game_types.rules
		FROM rounds
		JOIN game_types ON game_types.id = rounds.game_type_id
		WHERE rounds.status IN ('closed', 'settling')
		ORDER BY rounds.bet_closes_at, rounds.id
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
		if err := rows.Scan(&item.round.RoundID, &item.round.GameType, &item.round.Sequence, &item.round.Status, &item.round.BetClosesAt, &item.rules); err != nil {
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
			failures = append(failures, fmt.Errorf("round %s outcome: %w", item.round.RoundID, err))
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
