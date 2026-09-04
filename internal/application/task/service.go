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

var chinaTimeZone = time.FixedZone("China Standard Time", 8*60*60)

var ErrTaskConfigNotFound = errors.New("task config not found")
var ErrTaskNotCompleted = errors.New("task has not reached its claim threshold today")
var ErrTaskAlreadyClaimed = errors.New("task reward has already been claimed today")

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

type BetTaskClaim struct {
	TaskID            string    `json:"task_id"`
	BetDate           string    `json:"bet_date"`
	RewardCurrency    string    `json:"reward_currency"`
	RewardMinor       int64     `json:"reward_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	ClaimedAt         time.Time `json:"claimed_at"`
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
			return nil, errors.New("task currencies must be non-empty and threshold/reward must be positive")
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
			result, err := tx.Exec(ctx, `
				UPDATE bet_task_configs
				SET accumulation_currency=$2,reward_currency=$3,threshold_minor=$4,reward_minor=$5,enabled=$6
				WHERE id=$1`,
				item.ID, item.AccumulationCurrency, item.RewardCurrency, item.ThresholdMinor, item.RewardMinor, item.Enabled)
			if err != nil {
				return nil, err
			}
			if result.RowsAffected() == 0 {
				return nil, fmt.Errorf("%w: %s", ErrTaskConfigNotFound, item.ID)
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
	today := service.betDate()
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

// OnBetSettled 只累计最终结算为 won/lost 的有效投注。取消和退款不会调用此方法。
// 奖励仍须玩家在结算所属的中国时区自然日内主动领取。
func (service *Service) OnBetSettled(ctx context.Context, tx pgx.Tx, userID, currency string, stakeMinor int64, settledAt time.Time) error {
	today := service.betDateAt(settledAt)
	var configured bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bet_task_configs WHERE enabled=true AND accumulation_currency=$1)`, currency).Scan(&configured); err != nil {
		return err
	}
	if !configured {
		return nil
	}

	// 累计当前配置币种的当日投注总额。
	_, err := tx.Exec(ctx, `
		INSERT INTO user_daily_bet_progress (user_id, bet_date, accumulation_currency, total_stake_minor)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, bet_date, accumulation_currency)
		DO UPDATE SET total_stake_minor = user_daily_bet_progress.total_stake_minor + $4, updated_at = now()`,
		userID, today, currency, stakeMinor)
	return err
}

// ClaimBetTask 在中国时区当日领取一个已达标档位。领取记录、钱包余额和流水
// 位于同一事务；唯一约束保证同一用户、日期和档位只能领取一次。
func (service *Service) ClaimBetTask(ctx context.Context, userID, configID string) (BetTaskClaim, error) {
	if _, err := uuid.Parse(configID); err != nil {
		return BetTaskClaim{}, ErrTaskConfigNotFound
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return BetTaskClaim{}, err
	}
	defer tx.Rollback(ctx)

	today := service.betDate()
	var accumulationCurrency, rewardCurrency string
	var thresholdMinor, rewardMinor int64
	err = tx.QueryRow(ctx, `
		SELECT accumulation_currency,reward_currency,threshold_minor,reward_minor
		FROM bet_task_configs
		WHERE id=$1 AND enabled=true
		FOR UPDATE`, configID).Scan(&accumulationCurrency, &rewardCurrency, &thresholdMinor, &rewardMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return BetTaskClaim{}, ErrTaskConfigNotFound
	}
	if err != nil {
		return BetTaskClaim{}, err
	}

	var progressMinor int64
	err = tx.QueryRow(ctx, `
		SELECT total_stake_minor
		FROM user_daily_bet_progress
		WHERE user_id=$1 AND bet_date=$2 AND accumulation_currency=$3
		FOR UPDATE`, userID, today, accumulationCurrency).Scan(&progressMinor)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && progressMinor < thresholdMinor) {
		return BetTaskClaim{}, ErrTaskNotCompleted
	}
	if err != nil {
		return BetTaskClaim{}, err
	}

	claim := BetTaskClaim{
		TaskID:         configID,
		BetDate:        today,
		RewardCurrency: rewardCurrency,
		RewardMinor:    rewardMinor,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO bet_task_reward_records
			(id,user_id,bet_date,config_id,reward_minor,accumulation_currency,reward_currency)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(user_id,bet_date,config_id) DO NOTHING
		RETURNING created_at`, uuid.NewString(), userID, today, configID, rewardMinor, accumulationCurrency, rewardCurrency).Scan(&claim.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BetTaskClaim{}, ErrTaskAlreadyClaimed
	}
	if err != nil {
		return BetTaskClaim{}, err
	}

	bizID := fmt.Sprintf("bet_task:%s:%s:%s", today, accumulationCurrency, configID)
	remark := fmt.Sprintf("当日投注 %s 达 %d 手动领取 %s", accumulationCurrency, thresholdMinor, rewardCurrency)
	claim.BalanceAfterMinor, err = service.creditService.RewardCurrency(ctx, tx, userID, rewardCurrency, credit.BizBetTaskReward, bizID, rewardMinor, remark)
	if err != nil {
		return BetTaskClaim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BetTaskClaim{}, err
	}
	return claim, nil
}

func validAccumulationCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}
func validRewardCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}

func (service *Service) betDate() string {
	return service.betDateAt(service.now())
}

func (service *Service) betDateAt(value time.Time) string {
	return value.In(chinaTimeZone).Format("2006-01-02")
}
