package virtualbot

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"math"
	"math/big"
	"strconv"
	"strings"
)

type Service struct {
	pool *pgxpool.Pool
	bets *betting.Service
}

func NewService(pool *pgxpool.Pool, bets *betting.Service) *Service {
	return &Service{pool: pool, bets: bets}
}
func randomBetween(min, max int64) (int64, error) {
	if min < 0 || max < min || max-min == math.MaxInt64 {
		return 0, ErrInvalidPlan
	}
	n, e := rand.Int(rand.Reader, big.NewInt(max-min+1))
	if e != nil {
		return 0, e
	}
	return min + n.Int64(), nil
}

// Each plan advances by block-round sequence, never by elapsed seconds.
// A plan row lock, simulated bet and next sequence share one transaction.
func (s *Service) RunDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, e := s.pool.Query(ctx, `SELECT p.id::text FROM robot_plans p JOIN users u ON u.id=p.user_id
 WHERE p.enabled AND NOT p.deleted AND u.is_virtual AND u.status='active'
 AND (p.last_checked_at IS NULL OR p.last_checked_at<now()-interval '1 second')
 ORDER BY p.last_checked_at NULLS FIRST,p.id LIMIT $1`, limit)
	if e != nil {
		return 0, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	count := 0
	for _, id := range ids {
		n, e := s.runPlan(ctx, id)
		if e != nil {
			return count, e
		}
		count += n
	}
	return count, nil
}
func (s *Service) runPlan(ctx context.Context, id string) (int, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	v, e := scanPlan(tx.QueryRow(ctx, planSelect+` WHERE p.id=$1 AND p.enabled AND NOT p.deleted AND u.is_virtual AND u.status='active'
 AND (p.last_checked_at IS NULL OR p.last_checked_at<now()-interval '1 second') FOR UPDATE OF p SKIP LOCKED`, id))
	if errors.Is(e, ErrPlanNotFound) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	finish := func(status, message string, n int) (int, error) {
		_, e := tx.Exec(ctx, `UPDATE robot_plans SET last_checked_at=now(),last_status=$2,last_error=$3 WHERE id=$1`, id, status, message)
		if e != nil {
			return 0, e
		}
		return n, tx.Commit(ctx)
	}
	var round string
	var seq int64
	e = tx.QueryRow(ctx, `SELECT r.id::text,r.sequence FROM rounds r JOIN game_types gt ON gt.id=r.game_type_id
 WHERE gt.code=$1 AND gt.enabled AND r.status='open' AND r.bet_closes_at>now() ORDER BY r.sequence DESC LIMIT 1`, v.GameType).Scan(&round, &seq)
	if errors.Is(e, pgx.ErrNoRows) {
		return finish("waiting_round", "暂无可投注轮次", 0)
	}
	if e != nil {
		return 0, e
	}
	step, e := strconv.ParseInt(strings.TrimPrefix(v.GameType, "hash_"), 10, 64)
	if e != nil || step <= 0 {
		return finish("failed", "游戏类型无效", 0)
	}
	if v.NextRoundSequence != nil && *v.NextRoundSequence > seq {
		return finish("waiting_round", "", 0)
	}
	skip, e := randomBetween(int64(v.SkipMin), int64(v.SkipMax))
	if e != nil {
		return 0, e
	}
	if seq > math.MaxInt64-step*skip {
		return finish("failed", "期号超出范围", 0)
	}
	if _, e = tx.Exec(ctx, `UPDATE robot_plans SET next_round_sequence=$2,last_seen_sequence=$3 WHERE id=$1`, id, seq+step*skip, seq); e != nil {
		return 0, e
	}
	// New/missed plans schedule forward instead of backfilling old periods.
	if v.NextRoundSequence == nil || *v.NextRoundSequence < seq {
		return finish("scheduled", "", 0)
	}
	idx, e := randomBetween(0, int64(len(v.Selections)-1))
	if e != nil {
		return 0, e
	}
	pick := v.Selections[idx]
	amount, e := randomBetween(v.MinStakeMinor, v.MaxStakeMinor)
	if e != nil {
		return 0, e
	}
	selection, _ := json.Marshal(map[string]string{"pick": pick.Pick})
	sub, e := tx.Begin(ctx)
	if e != nil {
		return 0, e
	}
	var user string
	e = sub.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1 AND is_virtual`, v.UserID).Scan(&user)
	if e != nil {
		return 0, e
	}
	if _, e = sub.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) SELECT gen_random_uuid(),$1,code FROM currencies WHERE code=$2 AND enabled ON CONFLICT(user_id,currency) DO NOTHING`, user, v.Currency); e != nil {
		return 0, e
	}
	if _, e = validatePlanDB(ctx, sub, v.PlanInput); e != nil {
		if re := sub.Rollback(ctx); re != nil {
			return 0, re
		}
		if !errors.Is(e, ErrInvalidPlan) {
			return 0, e
		}
		return finish("failed", "房间、币种、玩法或金额配置已失效", 0)
	}
	bet, e := s.bets.PlaceRobotBetTx(ctx, sub, betting.PlaceBetRequest{RobotPlanID: id, ClientRequestID: "robot-" + id + "-" + round, RoundID: round, AccountID: user, Currency: v.Currency,
		GameRoomID: v.GameRoomID, PlayMode: pick.PlayMode, Selection: selection, StakeMinor: amount})
	if e != nil {
		if re := sub.Rollback(ctx); re != nil {
			return 0, re
		}
		message := ""
		switch {
		case errors.Is(e, game.ErrBettingClosed):
			message = "本期已封盘"
		case errors.Is(e, betting.ErrStakeOutsideLimits):
			message = "投注金额超过房间限额"
		case errors.Is(e, betting.ErrHashRoomConflict):
			message = "同一玩家本期已使用其他赔率房间"
		case errors.Is(e, betting.ErrAccountDisabled), errors.Is(e, betting.ErrBettingBanned):
			message = "账号已禁用或禁止投注"
		case errors.Is(e, betting.ErrHashRoomRequired):
			message = "房间或玩法不可用"
		}
		if message == "" {
			return 0, fmt.Errorf("robot plan %s: %w", id, e)
		}
		return finish("failed", message, 0)
	}
	if e = sub.Commit(ctx); e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `UPDATE robot_plans SET last_bet_id=$2 WHERE id=$1`, id, bet.BetID); e != nil {
		return 0, e
	}
	return finish("placed", "", 1)
}
