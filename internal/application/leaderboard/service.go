package leaderboard

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/domain/wallet"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidPeriod      = errors.New("period must be today, yesterday, this_week, or last_week")
	ErrInvalidCurrency    = errors.New("currency is required")
	ErrInvalidRewardRules = errors.New("invalid leaderboard reward rules")
	ErrRewardRuleConflict = errors.New("leaderboard reward rule version conflict")
)

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

type Reward struct {
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
}
type Entry struct {
	TotalPayoutMinor    *int64    `json:"total_payout_minor,omitempty"`
	NetWinMinor         *int64    `json:"net_win_minor,omitempty"`
	AvailableMinor      *int64    `json:"available_minor,omitempty"`
	TotalBet            string    `json:"total_bet"`
	TotalPayout         *string   `json:"total_payout,omitempty"`
	NetWin              *string   `json:"net_win,omitempty"`
	Available           *string   `json:"available,omitempty"`
	FirstBetAt          time.Time `json:"first_bet_at"`
	Rank                int       `json:"rank"`
	UserID              int64     `json:"user_id"`
	DisplayName         string    `json:"display_name"`
	AvatarURL           string    `json:"avatar_url"`
	IsVirtual           bool      `json:"is_virtual"`
	EffectiveStakeMinor int64     `json:"effective_stake_minor"`
	Reward              *Reward   `json:"reward,omitempty"`
}
type Board struct {
	Self        *Entry     `json:"self"`
	Decimals    int        `json:"decimals"`
	Period      string     `json:"period"`
	PeriodType  string     `json:"period_type"`
	Currency    string     `json:"currency"`
	StartsAt    time.Time  `json:"starts_at"`
	EndsAt      time.Time  `json:"ends_at"`
	Status      string     `json:"status"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	Items       []Entry    `json:"items"`
}
type RewardRule struct {
	RankFrom       int    `json:"rank_from"`
	RankTo         int    `json:"rank_to"`
	RewardCurrency string `json:"reward_currency"`
	RewardMinor    int64  `json:"reward_minor"`
	Enabled        bool   `json:"enabled"`
}
type RewardRuleSet struct {
	PeriodType string       `json:"period_type"`
	Currency   string       `json:"currency"`
	Version    int64        `json:"version"`
	Rules      []RewardRule `json:"rules"`
}
type RewardDistribution struct {
	ID             string    `json:"id"`
	PeriodType     string    `json:"period_type"`
	StartsAt       time.Time `json:"starts_at"`
	EndsAt         time.Time `json:"ends_at"`
	Currency       string    `json:"currency"`
	UserID         int64     `json:"user_id"`
	DisplayName    string    `json:"display_name"`
	Rank           int       `json:"rank"`
	RewardCurrency string    `json:"reward_currency"`
	RewardMinor    int64     `json:"reward_minor"`
	PaidAt         time.Time `json:"paid_at"`
}
type DistributionQuery struct {
	PeriodType, Currency, User string
	Limit, Offset              int
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool, now: time.Now} }

func periodBounds(kind string, at time.Time) (time.Time, time.Time) {
	local := at.In(shanghai)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai)
	if kind == "weekly" {
		start = start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
		return start.UTC(), start.AddDate(0, 0, 7).UTC()
	}
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

// Refresh keeps current China-time daily/weekly boards current and freezes ended boards only after all their bets settle.
func (s *Service) Refresh(ctx context.Context, at time.Time) error {
	for _, kind := range []string{"daily", "weekly"} {
		start, end := periodBounds(kind, at)
		if _, err := s.pool.Exec(ctx, `INSERT INTO leaderboard_periods(period_type,starts_at,ends_at) VALUES($1,$2,$3) ON CONFLICT(period_type,starts_at) DO NOTHING`, kind, start, end); err != nil {
			return err
		}
		if err := s.refreshPeriod(ctx, kind, start); err != nil {
			return err
		}
		rows, err := s.pool.Query(ctx, `SELECT starts_at FROM leaderboard_periods WHERE period_type=$1 AND status='running' AND ends_at<=$2 ORDER BY starts_at`, kind, at.UTC())
		if err != nil {
			return err
		}
		for rows.Next() {
			var oldStart time.Time
			if err := rows.Scan(&oldStart); err != nil {
				rows.Close()
				return err
			}
			var pending int
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM bets b JOIN leaderboard_periods p ON p.period_type=$1 AND p.starts_at=$2 WHERE b.status='accepted' AND b.created_at>=p.starts_at AND b.created_at<p.ends_at`, kind, oldStart).Scan(&pending); err != nil {
				rows.Close()
				return err
			}
			if pending > 0 {
				continue
			}
			if err := s.refreshPeriod(ctx, kind, oldStart); err != nil {
				rows.Close()
				return err
			}
			if _, err := s.pool.Exec(ctx, `UPDATE leaderboard_periods SET status='frozen',frozen_at=now() WHERE period_type=$1 AND starts_at=$2 AND status='running'`, kind, oldStart); err != nil {
				rows.Close()
				return err
			}
			if err := s.payPeriod(ctx, kind, oldStart); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Service) refreshPeriod(ctx context.Context, kind string, start time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id, status string
	if err = tx.QueryRow(ctx, `SELECT id::text,status FROM leaderboard_periods WHERE period_type=$1 AND starts_at=$2 FOR UPDATE`, kind, start).Scan(&id, &status); err != nil {
		return err
	}
	if status == "frozen" {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM leaderboard_entries WHERE period_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO leaderboard_entries(period_id,currency,user_id,public_user_id,display_name,avatar_url,is_virtual,effective_stake_minor,first_effective_at,total_payout_minor,available_minor)
		SELECT $1,w.currency,b.user_id,u.public_id,u.display_name,COALESCE(u.avatar_url,''),u.is_virtual,sum(b.stake_minor),min(b.created_at),sum(b.payout_minor),max(w.available_minor)
		FROM bets b JOIN wallets w ON w.id=b.wallet_id JOIN users u ON u.id=b.user_id JOIN leaderboard_periods p ON p.id=$1
		WHERE b.status IN ('won','lost') AND b.payout_minor>b.stake_minor AND b.created_at>=p.starts_at AND b.created_at<p.ends_at
		GROUP BY w.currency,b.user_id,u.public_id,u.display_name,u.avatar_url,u.is_virtual`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `WITH ranked AS (SELECT period_id,currency,user_id,row_number() OVER(PARTITION BY period_id,currency ORDER BY (total_payout_minor-effective_stake_minor) DESC,first_effective_at,public_user_id) r FROM leaderboard_entries WHERE period_id=$1) UPDATE leaderboard_entries e SET rank=r.r FROM ranked r WHERE e.period_id=r.period_id AND e.currency=r.currency AND e.user_id=r.user_id`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE leaderboard_periods SET refreshed_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) payPeriod(ctx context.Context, kind string, start time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT e.user_id::text,e.currency,e.rank,r.reward_currency,r.reward_minor,p.id::text,to_jsonb(r)||jsonb_build_object('version',v.version) FROM leaderboard_entries e JOIN leaderboard_periods p ON p.id=e.period_id JOIN leaderboard_reward_rules r ON r.period_type=p.period_type AND r.currency=e.currency AND e.rank BETWEEN r.rank_from AND r.rank_to LEFT JOIN leaderboard_reward_rule_versions v ON v.period_type=r.period_type AND v.currency=r.currency WHERE p.period_type=$1 AND p.starts_at=$2 AND r.enabled`, kind, start)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user, currency, rewardCurrency, periodID string
		var rank int
		var amount int64
		var ruleSnapshot []byte
		if err := rows.Scan(&user, &currency, &rank, &rewardCurrency, &amount, &periodID, &ruleSnapshot); err != nil {
			return err
		}
		if err := s.pay(ctx, periodID, currency, user, rank, rewardCurrency, amount, ruleSnapshot); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (s *Service) pay(ctx context.Context, periodID, currency, user string, rank int, rewardCurrency string, amount int64, ruleSnapshot []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leaderboard_reward_distributions WHERE period_id=$1 AND leaderboard_currency=$2 AND user_id=$3)`, periodID, currency, user).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,$3) ON CONFLICT(user_id,currency) DO NOTHING`, uuid.NewString(), user, rewardCurrency); err != nil {
		return err
	}
	var walletID string
	var balance int64
	if err = tx.QueryRow(ctx, `SELECT id::text,available_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, user, rewardCurrency).Scan(&walletID, &balance); err != nil {
		return err
	}
	balance += amount
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, balance); err != nil {
		return err
	}
	distributionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO leaderboard_reward_distributions(id,period_id,leaderboard_currency,user_id,rank,reward_currency,reward_minor,rule_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, distributionID, periodID, currency, user, rank, rewardCurrency, amount, ruleSnapshot); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,remark) VALUES($1,$2,'leaderboard_reward',$3,'leaderboard_reward_credit',$4,$5,'排行榜奖励')`, uuid.NewString(), walletID, distributionID, amount, balance)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload) VALUES($1,'leaderboard_reward',$2,'wallet.leaderboard_reward.paid',jsonb_build_object('user_id',$3,'currency',$4,'amount_minor',$5))`, uuid.NewString(), distributionID, user, rewardCurrency, amount)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) List(ctx context.Context, period, currency string, limit int) (Board, error) {
	return s.ListForUser(ctx, period, currency, limit, "")
}

func (s *Service) ListForUser(ctx context.Context, period, currency string, limit int, userID string) (Board, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return Board{}, ErrInvalidCurrency
	}
	kind := "daily"
	shift := 0
	switch period {
	case "today":
	case "yesterday":
		shift = -1
	case "this_week":
		kind = "weekly"
	case "last_week":
		kind = "weekly"
		shift = -7
	default:
		return Board{}, ErrInvalidPeriod
	}
	start, end := periodBounds(kind, s.now())
	if shift != 0 {
		start = start.AddDate(0, 0, shift)
		end = end.AddDate(0, 0, shift)
	}
	o := Board{Period: period, PeriodType: kind, Currency: currency, StartsAt: start, EndsAt: end, Status: "running", Items: []Entry{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Board{}, err
	}
	defer tx.Rollback(ctx)
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if err := tx.QueryRow(ctx, `SELECT decimals FROM currencies WHERE code=$1`, currency).Scan(&o.Decimals); err != nil {
		return Board{}, ErrInvalidCurrency
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text,status,refreshed_at FROM leaderboard_periods WHERE period_type=$1 AND starts_at=$2`, kind, start).Scan(&id, &o.Status, &o.RefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, nil
	}
	if err != nil {
		return Board{}, err
	}
	rows, err := tx.Query(ctx, `SELECT e.rank,e.public_user_id,e.display_name,e.avatar_url,e.is_virtual,e.effective_stake_minor,e.total_payout_minor,e.available_minor,e.first_effective_at,
		COALESCE(d.reward_currency,r.reward_currency),COALESCE(d.reward_minor,r.reward_minor),e.user_id::text=$4
		FROM leaderboard_entries e
		LEFT JOIN leaderboard_reward_distributions d ON d.period_id=e.period_id AND d.leaderboard_currency=e.currency AND d.user_id=e.user_id
		LEFT JOIN leaderboard_reward_rules r ON r.period_type=$5 AND r.currency=e.currency AND e.rank BETWEEN r.rank_from AND r.rank_to AND r.enabled
		WHERE e.period_id=$1 AND e.currency=$2 AND (e.rank<=$3 OR e.user_id::text=$4) ORDER BY e.rank`, id, currency, limit, userID, kind)
	if err != nil {
		return Board{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var c *string
		var a *int64
		var self bool
		if err := rows.Scan(&e.Rank, &e.UserID, &e.DisplayName, &e.AvatarURL, &e.IsVirtual, &e.EffectiveStakeMinor, &e.TotalPayoutMinor, &e.AvailableMinor, &e.FirstBetAt, &c, &a, &self); err != nil {
			return Board{}, err
		}
		if c != nil {
			e.Reward = &Reward{Currency: *c, AmountMinor: *a}
		}
		e.TotalBet, _ = wallet.FormatDisplayAmount(e.EffectiveStakeMinor, o.Decimals)
		if e.TotalPayoutMinor != nil {
			v := *e.TotalPayoutMinor - e.EffectiveStakeMinor
			e.NetWinMinor = &v
			t, _ := wallet.FormatDisplayAmount(*e.TotalPayoutMinor, o.Decimals)
			e.TotalPayout = &t
			n, _ := wallet.FormatDisplayAmount(v, o.Decimals)
			e.NetWin = &n
		}
		if e.AvailableMinor != nil {
			v, _ := wallet.FormatDisplayAmount(*e.AvailableMinor, o.Decimals)
			e.Available = &v
		}
		if self {
			copy := e
			o.Self = &copy
		}
		if e.Rank <= limit {
			o.Items = append(o.Items, e)
		}
	}
	return o, rows.Err()
}
func (s *Service) GetRules(ctx context.Context, kind, currency string) (RewardRuleSet, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if (kind != "daily" && kind != "weekly") || currency == "" {
		return RewardRuleSet{}, ErrInvalidRewardRules
	}
	o := RewardRuleSet{PeriodType: kind, Currency: currency, Rules: []RewardRule{}}
	err := s.pool.QueryRow(ctx, `SELECT version FROM leaderboard_reward_rule_versions WHERE period_type=$1 AND currency=$2`, kind, currency).Scan(&o.Version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return o, err
	}
	rows, err := s.pool.Query(ctx, `SELECT rank_from,rank_to,reward_currency,reward_minor,enabled FROM leaderboard_reward_rules WHERE period_type=$1 AND currency=$2 ORDER BY rank_from`, kind, currency)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var r RewardRule
		if err := rows.Scan(&r.RankFrom, &r.RankTo, &r.RewardCurrency, &r.RewardMinor, &r.Enabled); err != nil {
			return o, err
		}
		o.Rules = append(o.Rules, r)
	}
	return o, rows.Err()
}
func (s *Service) ReplaceRules(ctx context.Context, in RewardRuleSet) (RewardRuleSet, error) {
	in.PeriodType = strings.ToLower(strings.TrimSpace(in.PeriodType))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if err := validateRules(in); err != nil {
		return RewardRuleSet{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RewardRuleSet{}, err
	}
	defer tx.Rollback(ctx)
	var v int64
	err = tx.QueryRow(ctx, `SELECT version FROM leaderboard_reward_rule_versions WHERE period_type=$1 AND currency=$2 FOR UPDATE`, in.PeriodType, in.Currency).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		if in.Version != 0 {
			return RewardRuleSet{}, ErrRewardRuleConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO leaderboard_reward_rule_versions(period_type,currency,version) VALUES($1,$2,1)`, in.PeriodType, in.Currency)
		v = 1
	} else if err == nil {
		if v != in.Version {
			return RewardRuleSet{}, ErrRewardRuleConflict
		}
		_, err = tx.Exec(ctx, `UPDATE leaderboard_reward_rule_versions SET version=version+1 WHERE period_type=$1 AND currency=$2`, in.PeriodType, in.Currency)
		v++
	}
	if err != nil {
		return RewardRuleSet{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM leaderboard_reward_rules WHERE period_type=$1 AND currency=$2`, in.PeriodType, in.Currency); err != nil {
		return RewardRuleSet{}, err
	}
	for _, r := range in.Rules {
		if _, err = tx.Exec(ctx, `INSERT INTO leaderboard_reward_rules(period_type,currency,rank_from,rank_to,reward_currency,reward_minor,enabled) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.PeriodType, in.Currency, r.RankFrom, r.RankTo, strings.ToUpper(strings.TrimSpace(r.RewardCurrency)), r.RewardMinor, r.Enabled); err != nil {
			return RewardRuleSet{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return RewardRuleSet{}, err
	}
	in.Version = v
	return in, nil
}
func validateRules(in RewardRuleSet) error {
	if (in.PeriodType != "daily" && in.PeriodType != "weekly") || in.Currency == "" || in.Version < 0 {
		return ErrInvalidRewardRules
	}
	last := 0
	for _, r := range in.Rules {
		if r.RankFrom <= last || r.RankTo < r.RankFrom || strings.TrimSpace(r.RewardCurrency) == "" || r.RewardMinor <= 0 {
			return ErrInvalidRewardRules
		}
		last = r.RankTo
	}
	return nil
}
func (s *Service) ListDistributions(ctx context.Context, q DistributionQuery) ([]RewardDistribution, error) {
	if q.Limit < 1 || q.Limit > 200 {
		q.Limit = 50
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id::text,p.period_type,p.starts_at,p.ends_at,d.leaderboard_currency,u.public_id,u.display_name,d.rank,d.reward_currency,d.reward_minor,d.paid_at FROM leaderboard_reward_distributions d JOIN leaderboard_periods p ON p.id=d.period_id JOIN users u ON u.id=d.user_id WHERE ($1='' OR p.period_type=$1) AND ($2='' OR d.leaderboard_currency=$2) AND ($3='' OR u.public_id::text=$3 OR u.login_name ILIKE '%'||$3||'%') ORDER BY d.paid_at DESC LIMIT $4 OFFSET $5`, q.PeriodType, strings.ToUpper(q.Currency), q.User, q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RewardDistribution{}
	for rows.Next() {
		var x RewardDistribution
		if err := rows.Scan(&x.ID, &x.PeriodType, &x.StartsAt, &x.EndsAt, &x.Currency, &x.UserID, &x.DisplayName, &x.Rank, &x.RewardCurrency, &x.RewardMinor, &x.PaidAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
