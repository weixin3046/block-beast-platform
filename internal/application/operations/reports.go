package operations

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
)

type AdminBet struct {
	Balance          string          `json:"balance"`
	FrozenBalance    string          `json:"frozen_balance"`
	PlacementCount   int64           `json:"placement_count"`
	LastPlacedAt     time.Time       `json:"last_placed_at"`
	Decimals         int             `json:"decimals"`
	Stake            string          `json:"stake"`
	Payout           string          `json:"payout"`
	BetID            string          `json:"bet_id"`
	UserID           int64           `json:"user_id"`
	LoginName        string          `json:"login_name"`
	DisplayName      string          `json:"display_name"`
	IsVirtual        bool            `json:"is_virtual"`
	GameType         string          `json:"game_type"`
	GameName         string          `json:"game_name"`
	RoundSequence    int64           `json:"round_sequence"`
	Currency         string          `json:"currency"`
	GameRoomID       string          `json:"game_room_id,omitempty"`
	GameRoomCode     string          `json:"game_room_code,omitempty"`
	GameRoomName     string          `json:"game_room_name,omitempty"`
	PlayMode         string          `json:"play_mode,omitempty"`
	Selection        json.RawMessage `json:"selection"`
	StakeMinor       int64           `json:"stake_minor"`
	PayoutMultiplier int64           `json:"payout_multiplier"`
	PayoutDivisor    int64           `json:"payout_divisor"`
	PayoutRate       string          `json:"payout_rate"`
	PayoutMinor      int64           `json:"payout_minor"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
	SettledAt        *time.Time      `json:"settled_at,omitempty"`
}
type BetQuery struct {
	User, GameType, Currency, Status string
	From, To                         time.Time
	Limit, Offset                    int
}

func (s *Service) ListAdminBets(ctx context.Context, q BetQuery) ([]AdminBet, error) {
	normalizePage(&q.Limit, &q.Offset)
	rows, err := s.pool.Query(ctx, `
		SELECT b.id::text,u.public_id,COALESCE(u.login_name,''),u.display_name,u.is_virtual,
			gt.code,gt.name,r.sequence,w.currency,COALESCE(b.game_room_id::text,''),
			COALESCE(gr.code,''),COALESCE(gr.name,''),COALESCE(b.play_mode,''),b.selection,b.stake_minor,
			COALESCE(b.payout_multiplier_snapshot,0),COALESCE(b.payout_divisor_snapshot,0),
			b.payout_minor,b.status,b.created_at,b.settled_at,c.decimals,b.placement_count,COALESCE(b.last_placed_at,b.created_at),w.available_minor,w.frozen_minor
		FROM bets b JOIN users u ON u.id=b.user_id JOIN wallets w ON w.id=b.wallet_id
		JOIN currencies c ON c.code=w.currency
		JOIN rounds r ON r.id=b.round_id JOIN game_types gt ON gt.id=r.game_type_id
		LEFT JOIN game_rooms gr ON gr.id=b.game_room_id
		WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%')
			AND ($2='' OR gt.code=$2) AND ($3='' OR w.currency=$3) AND ($4='' OR b.status=$4)
			AND ($5::timestamptz IS NULL OR b.created_at >= $5) AND ($6::timestamptz IS NULL OR b.created_at < $6)
		ORDER BY b.created_at DESC,b.id DESC LIMIT $7 OFFSET $8`, q.User, q.GameType, q.Currency, q.Status, nullTime(q.From), nullTime(q.To), q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminBet{}
	for rows.Next() {
		var v AdminBet
		var available, frozen int64
		if err := rows.Scan(&v.BetID, &v.UserID, &v.LoginName, &v.DisplayName, &v.IsVirtual,
			&v.GameType, &v.GameName, &v.RoundSequence, &v.Currency, &v.GameRoomID, &v.GameRoomCode,
			&v.GameRoomName, &v.PlayMode, &v.Selection, &v.StakeMinor, &v.PayoutMultiplier,
			&v.PayoutDivisor, &v.PayoutMinor, &v.Status, &v.CreatedAt, &v.SettledAt, &v.Decimals, &v.PlacementCount, &v.LastPlacedAt, &available, &frozen); err != nil {
			return nil, err
		}
		if v.Balance, err = wallet.FormatDisplayAmount(available, v.Decimals); err != nil {
			return nil, err
		}
		if v.FrozenBalance, err = wallet.FormatDisplayAmount(frozen, v.Decimals); err != nil {
			return nil, err
		}
		v.PayoutRate = payoutRate(v.PayoutMultiplier, v.PayoutDivisor)
		if v.Stake, err = wallet.FormatDisplayAmount(v.StakeMinor, v.Decimals); err != nil {
			return nil, err
		}
		if v.Payout, err = wallet.FormatDisplayAmount(v.PayoutMinor, v.Decimals); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func payoutRate(multiplier, divisor int64) string {
	if multiplier <= 0 || divisor <= 0 {
		return ""
	}
	value := new(big.Rat).SetFrac64(multiplier, divisor).FloatString(6)
	return strings.TrimRight(strings.TrimRight(value, "0"), ".")
}

type LedgerRecord struct {
	DisplayName       string    `json:"display_name"`
	Decimals          int       `json:"decimals"`
	Amount            string    `json:"amount"`
	BalanceAfter      string    `json:"balance_after"`
	ID                string    `json:"id"`
	UserID            int64     `json:"user_id"`
	LoginName         string    `json:"login_name"`
	Currency          string    `json:"currency"`
	BusinessType      string    `json:"business_type"`
	BusinessID        string    `json:"business_id"`
	EntryType         string    `json:"entry_type"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Remark            string    `json:"remark"`
	OccurredAt        time.Time `json:"occurred_at"`
}
type LedgerQuery struct {
	User, Currency, BusinessType string
	From, To                     time.Time
	Limit, Offset                int
}

func (s *Service) ListAdminLedger(ctx context.Context, q LedgerQuery) ([]LedgerRecord, error) {
	normalizePage(&q.Limit, &q.Offset)
	rows, err := s.pool.Query(ctx, `SELECT le.id::text,u.public_id,COALESCE(u.login_name,''),w.currency,le.business_type,le.business_id,le.entry_type,le.amount_minor,le.balance_after_minor,le.remark,le.occurred_at,u.display_name,c.decimals
        FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id JOIN users u ON u.id=w.user_id
        JOIN currencies c ON c.code=w.currency
        WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') AND ($2='' OR w.currency=$2) AND ($3='' OR le.business_type=$3)
        AND ($4::timestamptz IS NULL OR le.occurred_at >= $4) AND ($5::timestamptz IS NULL OR le.occurred_at < $5)
        ORDER BY le.occurred_at DESC,le.id DESC LIMIT $6 OFFSET $7`, q.User, q.Currency, q.BusinessType, nullTime(q.From), nullTime(q.To), q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerRecord{}
	for rows.Next() {
		var v LedgerRecord
		if err := rows.Scan(&v.ID, &v.UserID, &v.LoginName, &v.Currency, &v.BusinessType, &v.BusinessID, &v.EntryType, &v.AmountMinor, &v.BalanceAfterMinor, &v.Remark, &v.OccurredAt, &v.DisplayName, &v.Decimals); err != nil {
			return nil, err
		}
		if v.Amount, err = wallet.FormatDisplayAmount(v.AmountMinor, v.Decimals); err != nil {
			return nil, err
		}
		if v.BalanceAfter, err = wallet.FormatDisplayAmount(v.BalanceAfterMinor, v.Decimals); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type RefundClearanceRecord struct {
	ID                string    `json:"id"`
	RecordType        string    `json:"record_type"`
	UserID            int64     `json:"user_id"`
	LoginName         string    `json:"login_name"`
	Currency          string    `json:"currency"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Status            string    `json:"status"`
	BetID             string    `json:"bet_id,omitempty"`
	GameType          string    `json:"game_type,omitempty"`
	GameName          string    `json:"game_name,omitempty"`
	RoundSequence     *int64    `json:"round_sequence,omitempty"`
	GameRoomID        string    `json:"game_room_id,omitempty"`
	GameRoomCode      string    `json:"game_room_code,omitempty"`
	GameRoomName      string    `json:"game_room_name,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

func (s *Service) ListRefundClearances(ctx context.Context, user, status string, from, to time.Time, limit, offset int) ([]RefundClearanceRecord, error) {
	normalizePage(&limit, &offset)
	rows, err := s.pool.Query(ctx, `
		SELECT x.id,x.record_type,u.public_id,COALESCE(u.login_name,''),x.currency,x.amount_minor,
			x.balance_after_minor,x.status,COALESCE(x.bet_id,''),COALESCE(gt.code,''),COALESCE(gt.name,''),
			r.sequence,COALESCE(b.game_room_id::text,''),COALESCE(gr.code,''),COALESCE(gr.name,''),x.occurred_at
		FROM (
			SELECT le.id::text,'bet_refund' record_type,w.user_id,w.currency,le.amount_minor,
				le.balance_after_minor,'completed' status,le.business_id bet_id,le.occurred_at
			FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id
			WHERE le.entry_type IN ('refund','bet_refund')
			UNION ALL
			SELECT le.id::text,'bet_void',w.user_id,w.currency,le.amount_minor,
				le.balance_after_minor,'completed',v.bet_id::text,le.occurred_at
			FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id
			JOIN admin_bet_voids v ON v.id::text=le.business_id
			WHERE le.business_type='bet_void' AND le.entry_type='bet_void_refund'
			UNION ALL
			SELECT le.id::text,'admin_debit',w.user_id,w.currency,-le.amount_minor,
				le.balance_after_minor,'completed',''::text,le.occurred_at
			FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id WHERE le.business_type='admin_debit'
			UNION ALL
			SELECT wd.id::text,'withdrawal',wd.user_id,w.currency,wd.amount_minor,0,wd.status,''::text,wd.created_at
			FROM withdrawals wd JOIN wallets w ON w.id=wd.wallet_id
			UNION ALL
			SELECT pw.id::text,'point_withdrawal',pw.user_id,'POINTS',pw.amount_minor,0,pw.status,''::text,pw.created_at
			FROM point_withdrawals pw
		) x
		JOIN users u ON u.id=x.user_id
		LEFT JOIN bets b ON b.id::text=x.bet_id
		LEFT JOIN rounds r ON r.id=b.round_id
		LEFT JOIN game_types gt ON gt.id=r.game_type_id
		LEFT JOIN game_rooms gr ON gr.id=b.game_room_id
		WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%')
			AND ($2='' OR x.status=$2) AND ($3::timestamptz IS NULL OR x.occurred_at >= $3)
			AND ($4::timestamptz IS NULL OR x.occurred_at < $4)
		ORDER BY x.occurred_at DESC LIMIT $5 OFFSET $6`, user, status, nullTime(from), nullTime(to), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RefundClearanceRecord{}
	for rows.Next() {
		var v RefundClearanceRecord
		if err := rows.Scan(&v.ID, &v.RecordType, &v.UserID, &v.LoginName, &v.Currency,
			&v.AmountMinor, &v.BalanceAfterMinor, &v.Status, &v.BetID, &v.GameType, &v.GameName,
			&v.RoundSequence, &v.GameRoomID, &v.GameRoomCode, &v.GameRoomName, &v.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func normalizePage(limit, offset *int) {
	if *limit <= 0 || *limit > 200 {
		*limit = 50
	}
	if *offset < 0 {
		*offset = 0
	}
}
func nullTime(v time.Time) any {
	if v.IsZero() {
		return nil
	}
	return v
}
