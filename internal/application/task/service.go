package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool          *pgxpool.Pool
	creditService *credit.Service
	now           func() time.Time
}

func NewService(pool *pgxpool.Pool, creditService *credit.Service) *Service {
	return &Service{pool: pool, creditService: creditService, now: time.Now}
}

type BetTask struct {
	ID                   string `json:"id"`
	AccumulationCurrency string `json:"accumulation_currency"`
	RewardCurrency       string `json:"reward_currency"`
	ThresholdMinor       int64  `json:"threshold_minor"`
	RewardMinor          int64  `json:"reward_minor"`
	ProgressMinor        int64  `json:"progress_minor"`
	Completed            bool   `json:"completed"`
	Rewarded             bool   `json:"rewarded"`
}

type BetTaskConfig struct {
	ID                   string `json:"id"`
	AccumulationCurrency string `json:"accumulation_currency"`
	RewardCurrency       string `json:"reward_currency"`
	ThresholdMinor       int64  `json:"threshold_minor"`
	RewardMinor          int64  `json:"reward_minor"`
	Enabled              bool   `json:"enabled"`
}

func (service *Service) BetTaskConfigs(ctx context.Context) ([]BetTaskConfig, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,accumulation_currency,reward_currency,threshold_minor,reward_minor,enabled
		FROM bet_task_configs ORDER BY accumulation_currency,threshold_minor`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]BetTaskConfig, 0)
	for rows.Next() {
		var item BetTaskConfig
		if err := rows.Scan(&item.ID, &item.AccumulationCurrency, &item.RewardCurrency, &item.ThresholdMinor, &item.RewardMinor, &item.Enabled); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) ReplaceBetTaskConfigs(ctx context.Context, items []BetTaskConfig) ([]BetTaskConfig, error) {
	if len(items) == 0 {
		return nil, errors.New("at least one task config is required")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	keep := make([]string, 0, len(items))
	for _, item := range items {
		item.AccumulationCurrency = strings.ToUpper(strings.TrimSpace(item.AccumulationCurrency))
		item.RewardCurrency = strings.ToUpper(strings.TrimSpace(item.RewardCurrency))
		if item.ThresholdMinor <= 0 || item.RewardMinor <= 0 || !validAccumulationCurrency(item.AccumulationCurrency) || !validRewardCurrency(item.RewardCurrency) {
			return nil, errors.New("task currencies must be supported and threshold/reward must be positive")
		}
		if item.ID == "" {
			item.ID = uuid.NewString()
			if _, err := tx.Exec(ctx, `
				INSERT INTO bet_task_configs(id,accumulation_currency,reward_currency,threshold_minor,reward_minor,enabled)
				VALUES($1,$2,$3,$4,$5,$6)`,
				item.ID, item.AccumulationCurrency, item.RewardCurrency, item.ThresholdMinor, item.RewardMinor, item.Enabled); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE bet_task_configs
				SET accumulation_currency=$2,reward_currency=$3,threshold_minor=$4,reward_minor=$5,enabled=$6
				WHERE id=$1`,
				item.ID, item.AccumulationCurrency, item.RewardCurrency, item.ThresholdMinor, item.RewardMinor, item.Enabled); err != nil {
				return nil, err
			}
		}
		keep = append(keep, item.ID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE bet_task_configs SET enabled=false WHERE NOT (id::text = ANY($1))`, keep); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return service.BetTaskConfigs(ctx)
}

func (service *Service) BetTasks(ctx context.Context, userID string) ([]BetTask, error) {
	today := service.now().UTC().Format("2006-01-02")
	rows, err := service.pool.Query(ctx, `
		SELECT c.id::text,c.accumulation_currency,c.reward_currency,c.threshold_minor,c.reward_minor,
			COALESCE(p.total_stake_minor,0),
			COALESCE(p.total_stake_minor,0) >= c.threshold_minor,
			EXISTS(
				SELECT 1 FROM bet_task_reward_records r
				WHERE r.user_id=$1 AND r.bet_date=$2 AND r.config_id=c.id
			)
		FROM bet_task_configs c
		LEFT JOIN user_daily_bet_progress p
			ON p.user_id=$1 AND p.bet_date=$2 AND p.accumulation_currency=c.accumulation_currency
		WHERE c.enabled
		ORDER BY c.accumulation_currency,c.threshold_minor`, userID, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]BetTask, 0)
	for rows.Next() {
		var item BetTask
		if err := rows.Scan(
			&item.ID, &item.AccumulationCurrency, &item.RewardCurrency, &item.ThresholdMinor, &item.RewardMinor,
			&item.ProgressMinor, &item.Completed, &item.Rewarded,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// OnBetPlaced 按配置币种累计当日进度，并对新达标的档位发放配置币种奖励。
// 在 betting service 的投注事务内调用，任一档位失败则整体回滚。
func (service *Service) OnBetPlaced(ctx context.Context, tx pgx.Tx, userID, currency string, stakeMinor int64) error {
	today := service.now().UTC().Format("2006-01-02")
	var configured bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bet_task_configs WHERE enabled=true AND accumulation_currency=$1)`, currency).Scan(&configured); err != nil {
		return err
	}
	if !configured {
		return nil
	}

	// 累计当前配置币种的当日投注总额。
	var totalStake int64
	err := tx.QueryRow(ctx, `
		INSERT INTO user_daily_bet_progress (user_id, bet_date, accumulation_currency, total_stake_minor)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, bet_date, accumulation_currency)
		DO UPDATE SET total_stake_minor = user_daily_bet_progress.total_stake_minor + $4, updated_at = now()
		RETURNING total_stake_minor`, userID, today, currency, stakeMinor).Scan(&totalStake)
	if err != nil {
		return err
	}

	// 查找已达标但未领取的档位。
	rows, err := tx.Query(ctx, `
		SELECT c.id, c.threshold_minor, c.reward_minor, c.reward_currency
		FROM bet_task_configs c
		WHERE c.enabled AND c.accumulation_currency=$4 AND c.threshold_minor <= $1
			AND NOT EXISTS (
				SELECT 1 FROM bet_task_reward_records r
				WHERE r.user_id = $2 AND r.bet_date = $3 AND r.config_id = c.id
			)
		ORDER BY c.threshold_minor`, totalStake, userID, today, currency)
	if err != nil {
		return err
	}
	defer rows.Close()

	type reward struct {
		configID  string
		threshold int64
		amount    int64
		currency  string
	}
	rewards := make([]reward, 0)
	for rows.Next() {
		var r reward
		if err := rows.Scan(&r.configID, &r.threshold, &r.amount, &r.currency); err != nil {
			return err
		}
		rewards = append(rewards, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 逐档发放：先写奖励记录（唯一约束兜底），再发体力。
	for _, r := range rewards {
		if _, err := tx.Exec(ctx, `
			INSERT INTO bet_task_reward_records (id, user_id, bet_date, config_id, reward_minor, accumulation_currency, reward_currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, uuid.NewString(), userID, today, r.configID, r.amount, currency, r.currency); err != nil {
			return err
		}
		bizID := fmt.Sprintf("bet_task:%s:%s:%s", today, currency, r.configID)
		remark := fmt.Sprintf("当日投注 %s 达 %d 奖励 %s", currency, r.threshold, r.currency)
		if _, err := service.creditService.RewardCurrency(ctx, tx, userID, r.currency, credit.BizBetTaskReward, bizID, r.amount, remark); err != nil {
			return err
		}
	}
	return nil
}

func validAccumulationCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}
func validRewardCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}
