package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/block-beast/platform/internal/application/credit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAlreadyCheckedIn = errors.New("already checked in today")

// DefaultCheckinReward 是每日签到默认发放的体力值。
const DefaultCheckinReward int64 = 10

type Service struct {
	pool          *pgxpool.Pool
	creditService *credit.Service
	checkinReward int64
	now           func() time.Time
}

func NewService(pool *pgxpool.Pool, creditService *credit.Service) *Service {
	return &Service{pool: pool, creditService: creditService, checkinReward: DefaultCheckinReward, now: time.Now}
}

// WithCheckinReward 覆盖默认签到奖励值，便于测试与运营调整。
func (service *Service) WithCheckinReward(reward int64) *Service {
	service.checkinReward = reward
	return service
}

type CheckinResult struct {
	UserID            string    `json:"user_id"`
	CheckinDate       string    `json:"checkin_date"`
	RewardMinor       int64     `json:"reward_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	CheckedIn         bool      `json:"checked_in"` // false 表示今日已签到
	OccurredAt        time.Time `json:"occurred_at"`
}

// Checkin 每日签到：按 (user_id, checkin_date) 幂等，首次签到发放体力奖励。
func (service *Service) Checkin(ctx context.Context, userID string) (CheckinResult, error) {
	today := service.now().UTC().Format("2006-01-02")

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return CheckinResult{}, err
	}
	defer tx.Rollback(ctx)

	// 幂等：今日已签到则直接返回。
	var existing CheckinResult
	err = tx.QueryRow(ctx, `
		SELECT user_id, checkin_date::text, reward_minor, created_at
		FROM checkin_records WHERE user_id = $1 AND checkin_date = $2`, userID, today).
		Scan(&existing.UserID, &existing.CheckinDate, &existing.RewardMinor, &existing.OccurredAt)
	if err == nil {
		existing.CheckedIn = false
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CheckinResult{}, err
	}

	// 创建签到记录。
	if _, err := tx.Exec(ctx, `
		INSERT INTO checkin_records (id, user_id, checkin_date, reward_minor)
		VALUES ($1, $2, $3, $4)`, uuid.NewString(), userID, today, service.checkinReward); err != nil {
		return CheckinResult{}, err
	}

	// 发放体力奖励（同事务写 wallets + stamina_ledger）。
	bizID := "checkin:" + today
	balanceAfter, err := service.creditService.RewardStamina(ctx, tx, userID, credit.BizCheckinReward, bizID, service.checkinReward, "每日签到奖励")
	if err != nil {
		return CheckinResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CheckinResult{}, err
	}
	return CheckinResult{
		UserID:            userID,
		CheckinDate:       today,
		RewardMinor:       service.checkinReward,
		BalanceAfterMinor: balanceAfter,
		CheckedIn:         true,
		OccurredAt:        time.Now().UTC(),
	}, nil
}

// OnPointsBetPlaced 在积分投注成功后累计当日进度，并对新达标的档位发放体力奖励。
// 在 betting service 的投注事务内调用，任一档位失败则整体回滚。
func (service *Service) OnPointsBetPlaced(ctx context.Context, tx pgx.Tx, userID string, stakeMinor int64) error {
	today := service.now().UTC().Format("2006-01-02")

	// 累计当日投注积分总额。
	var totalStake int64
	err := tx.QueryRow(ctx, `
		INSERT INTO user_daily_bet_progress (user_id, bet_date, total_stake_minor)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, bet_date)
		DO UPDATE SET total_stake_minor = user_daily_bet_progress.total_stake_minor + $3, updated_at = now()
		RETURNING total_stake_minor`, userID, today, stakeMinor).Scan(&totalStake)
	if err != nil {
		return err
	}

	// 查找已达标但未领取的档位。
	rows, err := tx.Query(ctx, `
		SELECT c.id, c.threshold_minor, c.reward_minor
		FROM bet_task_configs c
		WHERE c.enabled AND c.threshold_minor <= $1
			AND NOT EXISTS (
				SELECT 1 FROM bet_task_reward_records r
				WHERE r.user_id = $2 AND r.bet_date = $3 AND r.config_id = c.id
			)
		ORDER BY c.threshold_minor`, totalStake, userID, today)
	if err != nil {
		return err
	}
	defer rows.Close()

	type reward struct {
		configID  string
		threshold int64
		amount    int64
	}
	rewards := make([]reward, 0)
	for rows.Next() {
		var r reward
		if err := rows.Scan(&r.configID, &r.threshold, &r.amount); err != nil {
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
			INSERT INTO bet_task_reward_records (id, user_id, bet_date, config_id, reward_minor)
			VALUES ($1, $2, $3, $4, $5)`, uuid.NewString(), userID, today, r.configID, r.amount); err != nil {
			return err
		}
		bizID := fmt.Sprintf("bet_task:%s:%s", today, r.configID)
		remark := fmt.Sprintf("当日投注积分达 %d 奖励", r.threshold)
		if _, err := service.creditService.RewardStamina(ctx, tx, userID, credit.BizBetTaskReward, bizID, r.amount, remark); err != nil {
			return err
		}
	}
	return nil
}
