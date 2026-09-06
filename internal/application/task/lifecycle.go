package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sort"
	"strings"
	"time"
)

var ErrInvalidTask = errors.New("任务配置参数无效")

type TaskReward struct {
	Currency          string `json:"currency"`
	AmountMinor       int64  `json:"amount_minor"`
	BalanceAfterMinor *int64 `json:"balance_after_minor,omitempty"`
}

const configSelect = `SELECT id::text,accumulation_currency,reward_currency,threshold_minor,reward_minor,enabled,title,period_type,sort_order,max_complete_count,rewards FROM bet_task_configs `

type scanner interface{ Scan(...any) error }

func scanConfig(row scanner) (BetTaskConfig, error) {
	var c BetTaskConfig
	var raw []byte
	e := row.Scan(&c.ID, &c.AccumulationCurrency, &c.RewardCurrency, &c.ThresholdMinor, &c.RewardMinor, &c.Enabled, &c.Title, &c.PeriodType, &c.SortOrder, &c.MaxCompleteCount, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, ErrTaskConfigNotFound
	}
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(raw, &c.Rewards); e != nil {
		return c, e
	}
	if len(c.Rewards) == 0 {
		c.Rewards = []TaskReward{{Currency: c.RewardCurrency, AmountMinor: c.RewardMinor}}
	}
	return c, nil
}
func (s *Service) BetTaskConfigs(ctx context.Context) ([]BetTaskConfig, error) {
	rows, e := s.pool.Query(ctx, configSelect+"WHERE NOT deleted ORDER BY sort_order,created_at,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BetTaskConfig{}
	for rows.Next() {
		c, e := scanConfig(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func normalizeConfig(c *BetTaskConfig) error {
	c.AccumulationCurrency = strings.ToUpper(strings.TrimSpace(c.AccumulationCurrency))
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" {
		c.Title = "投注任务"
	}
	if c.PeriodType == "" {
		c.PeriodType = "daily"
	}
	if len(c.Rewards) == 0 {
		c.Rewards = []TaskReward{{Currency: c.RewardCurrency, AmountMinor: c.RewardMinor}}
	}
	if c.ThresholdMinor <= 0 || c.AccumulationCurrency == "" || (c.PeriodType != "daily" && c.PeriodType != "global") || c.MaxCompleteCount < 0 || c.MaxCompleteCount > 100000 || len(c.Rewards) > 32 || len([]rune(c.Title)) > 100 {
		return ErrInvalidTask
	}
	seen := map[string]bool{}
	for i := range c.Rewards {
		p := &c.Rewards[i]
		p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
		if p.Currency == "" || p.AmountMinor <= 0 || p.BalanceAfterMinor != nil || seen[p.Currency] {
			return ErrInvalidTask
		}
		seen[p.Currency] = true
	}
	c.RewardCurrency = c.Rewards[0].Currency
	c.RewardMinor = c.Rewards[0].AmountMinor
	return nil
}
func (s *Service) SaveBetTaskConfig(ctx context.Context, c BetTaskConfig) (BetTaskConfig, error) {
	if e := normalizeConfig(&c); e != nil {
		return c, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return c, e
	}
	defer tx.Rollback(ctx)
	codes := []string{c.AccumulationCurrency}
	for _, p := range c.Rewards {
		codes = append(codes, p.Currency)
	}
	for _, code := range codes {
		var valid bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM currencies WHERE code=$1 AND enabled)", code).Scan(&valid); e != nil {
			return c, e
		}
		if !valid {
			return c, ErrInvalidTask
		}
	}
	raw, _ := json.Marshal(c.Rewards)
	if c.ID == "" {
		c.ID = uuid.NewString()
		_, e = tx.Exec(ctx, `INSERT INTO bet_task_configs(id,accumulation_currency,reward_currency,threshold_minor,reward_minor,enabled,title,period_type,sort_order,max_complete_count,rewards) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, c.ID, c.AccumulationCurrency, c.RewardCurrency, c.ThresholdMinor, c.RewardMinor, c.Enabled, c.Title, c.PeriodType, c.SortOrder, c.MaxCompleteCount, raw)
	} else {
		if _, e = uuid.Parse(c.ID); e != nil {
			return c, ErrTaskConfigNotFound
		}
		old, err := scanConfig(tx.QueryRow(ctx, configSelect+"WHERE id=$1 AND NOT deleted FOR UPDATE", c.ID))
		if err != nil {
			return c, err
		}
		// A task's accumulation unit and period must not reinterpret existing progress.
		if old.AccumulationCurrency != c.AccumulationCurrency || old.PeriodType != c.PeriodType {
			var exists bool
			if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM task_progress WHERE config_id=$1)", c.ID).Scan(&exists); e != nil {
				return c, e
			}
			if exists {
				return c, ErrInvalidTask
			}
		}
		_, e = tx.Exec(ctx, `UPDATE bet_task_configs SET accumulation_currency=$2,reward_currency=$3,threshold_minor=$4,reward_minor=$5,enabled=$6,title=$7,period_type=$8,sort_order=$9,max_complete_count=$10,rewards=$11 WHERE id=$1`, c.ID, c.AccumulationCurrency, c.RewardCurrency, c.ThresholdMinor, c.RewardMinor, c.Enabled, c.Title, c.PeriodType, c.SortOrder, c.MaxCompleteCount, raw)
	}
	if e != nil {
		return c, e
	}
	return c, tx.Commit(ctx)
}

// Retained only to satisfy older internal integrations; public bulk API is retired.
func (s *Service) ReplaceBetTaskConfigs(ctx context.Context, items []BetTaskConfig) ([]BetTaskConfig, error) {
	return nil, ErrInvalidTask
}
func periodDate(kind, date string) string {
	if kind == "global" {
		return "1970-01-01"
	}
	return date
}
func (s *Service) BetTasks(ctx context.Context, user string) ([]BetTask, error) {
	rows, e := s.pool.Query(ctx, `SELECT c.id::text,c.accumulation_currency,c.reward_currency,c.threshold_minor,c.reward_minor,c.enabled,c.title,c.period_type,c.sort_order,c.max_complete_count,c.rewards,COALESCE(p.progress_minor,0),COALESCE(p.complete_count,0)
 FROM bet_task_configs c LEFT JOIN task_progress p ON p.config_id=c.id AND p.user_id=$1 AND p.period_date=CASE WHEN c.period_type='global' THEN DATE '1970-01-01' ELSE $2::date END
 WHERE c.enabled AND NOT c.deleted ORDER BY c.sort_order,c.created_at,c.id`, user, s.betDate())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BetTask{}
	for rows.Next() {
		var c BetTask
		var enabled bool
		var raw []byte
		if e = rows.Scan(&c.ID, &c.AccumulationCurrency, &c.RewardCurrency, &c.ThresholdMinor, &c.RewardMinor, &enabled, &c.Title, &c.PeriodType, &c.SortOrder, &c.MaxCompleteCount, &raw, &c.ProgressMinor, &c.CompleteCount); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &c.Rewards); e != nil {
			return nil, e
		}
		if len(c.Rewards) == 0 {
			c.Rewards = []TaskReward{{Currency: c.RewardCurrency, AmountMinor: c.RewardMinor}}
		}
		c.Rewarded = c.MaxCompleteCount > 0 && c.CompleteCount >= c.MaxCompleteCount
		c.Completed = c.Rewarded || c.ProgressMinor >= c.ThresholdMinor
		out = append(out, c)
	}
	return out, rows.Err()
}

// Caller invokes only for real, non-dodge won/lost bets, passing placed_at.
func (s *Service) OnBetSettled(ctx context.Context, tx pgx.Tx, user, currency string, stake int64, placedAt time.Time) error {
	if stake <= 0 {
		return ErrInvalidTask
	}
	rows, e := tx.Query(ctx, configSelect+"WHERE enabled AND NOT deleted AND accumulation_currency=$1 ORDER BY id FOR SHARE", currency)
	if e != nil {
		return e
	}
	configs := []BetTaskConfig{}
	for rows.Next() {
		c, e := scanConfig(rows)
		if e != nil {
			rows.Close()
			return e
		}
		configs = append(configs, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, c := range configs {
		date := periodDate(c.PeriodType, s.betDateAt(placedAt))
		_, e = tx.Exec(ctx, `INSERT INTO task_progress(config_id,user_id,period_date,progress_minor) VALUES($1,$2,$3,least($4::bigint,$5::bigint))
 ON CONFLICT(config_id,user_id,period_date) DO UPDATE SET progress_minor=task_progress.progress_minor+least($4::bigint,greatest(0,$5::bigint-task_progress.progress_minor)),updated_at=now()
 WHERE task_progress.progress_minor<$5 AND ($6::int=0 OR task_progress.complete_count<$6)`, c.ID, user, date, stake, c.ThresholdMinor, c.MaxCompleteCount)
		if e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) ClaimBetTask(ctx context.Context, user, id string) (BetTaskClaim, error) {
	var out BetTaskClaim
	if _, e := uuid.Parse(id); e != nil {
		return out, ErrTaskConfigNotFound
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	c, e := scanConfig(tx.QueryRow(ctx, configSelect+"WHERE id=$1 AND enabled AND NOT deleted FOR SHARE", id))
	if e != nil {
		return out, e
	}
	date := periodDate(c.PeriodType, s.betDate())
	// Match the settlement wallet-before-progress lock order.
	rows, e := tx.Query(ctx, "SELECT id FROM wallets WHERE user_id=$1 ORDER BY id FOR UPDATE", user)
	if e != nil {
		return out, e
	}
	for rows.Next() {
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var progress int64
	var count int
	e = tx.QueryRow(ctx, "SELECT progress_minor,complete_count FROM task_progress WHERE config_id=$1 AND user_id=$2 AND period_date=$3 FOR UPDATE", id, user, date).Scan(&progress, &count)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrTaskNotCompleted
	}
	if e != nil {
		return out, e
	}
	if c.MaxCompleteCount > 0 && count >= c.MaxCompleteCount {
		return out, ErrTaskAlreadyClaimed
	}
	if progress < c.ThresholdMinor {
		return out, ErrTaskNotCompleted
	}
	if count >= 2147483647 {
		return out, ErrTaskAlreadyClaimed
	}
	out = BetTaskClaim{TaskID: id, BetDate: date, CompleteCount: count + 1, Rewards: append([]TaskReward(nil), c.Rewards...)}
	// Deterministic currency order for newly created reward wallets.
	sort.Slice(out.Rewards, func(i, j int) bool { return out.Rewards[i].Currency < out.Rewards[j].Currency })
	claimID := uuid.NewString()
	for i := range out.Rewards {
		p := &out.Rewards[i]
		balance, err := s.creditService.RewardCurrency(ctx, tx, user, p.Currency, credit.BizBetTaskReward, claimID+":"+p.Currency, p.AmountMinor, fmt.Sprintf("任务 %s 第%d次手动领取", c.Title, count+1))
		if err != nil {
			return out, err
		}
		p.BalanceAfterMinor = &balance
		if p.Currency == c.RewardCurrency {
			out.RewardCurrency = p.Currency
			out.RewardMinor = p.AmountMinor
			out.BalanceAfterMinor = balance
		}
	}
	raw, _ := json.Marshal(out.Rewards)
	snapshot, _ := json.Marshal(c)
	if e = tx.QueryRow(ctx, `INSERT INTO task_reward_claims(id,config_id,user_id,period_date,complete_count,rewards,config_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, claimID, id, user, date, count+1, raw, snapshot).Scan(&out.ClaimedAt); e != nil {
		return out, e
	}
	// Paid progress must never become claimable again after a cap increase.
	next := int64(0)
	if _, e = tx.Exec(ctx, "UPDATE task_progress SET progress_minor=$4,complete_count=$5,updated_at=now() WHERE config_id=$1 AND user_id=$2 AND period_date=$3", id, user, date, next, count+1); e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
