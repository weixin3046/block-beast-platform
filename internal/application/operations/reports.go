package operations

import (
	"context"
	"encoding/json"
	"time"
)

type AdminBet struct {
	BetID         string          `json:"bet_id"`
	UserID        int64           `json:"user_id"`
	LoginName     string          `json:"login_name"`
	GameType      string          `json:"game_type"`
	RoundSequence int64           `json:"round_sequence"`
	Currency      string          `json:"currency"`
	Selection     json.RawMessage `json:"selection"`
	StakeMinor    int64           `json:"stake_minor"`
	PayoutMinor   int64           `json:"payout_minor"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	SettledAt     *time.Time      `json:"settled_at,omitempty"`
}
type BetQuery struct {
	User, GameType, Currency, Status string
	From, To                         time.Time
	Limit, Offset                    int
}

func (s *Service) ListAdminBets(ctx context.Context, q BetQuery) ([]AdminBet, error) {
	normalizePage(&q.Limit, &q.Offset)
	rows, err := s.pool.Query(ctx, `SELECT b.id::text,u.public_id,COALESCE(u.login_name,''),gt.code,r.sequence,w.currency,b.selection,b.stake_minor,b.payout_minor,b.status,b.created_at,b.settled_at FROM bets b JOIN users u ON u.id=b.user_id JOIN wallets w ON w.id=b.wallet_id JOIN rounds r ON r.id=b.round_id JOIN game_types gt ON gt.id=r.game_type_id WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') AND ($2='' OR gt.code=$2) AND ($3='' OR w.currency=$3) AND ($4='' OR b.status=$4) AND ($5::timestamptz IS NULL OR b.created_at >= $5) AND ($6::timestamptz IS NULL OR b.created_at < $6) ORDER BY b.created_at DESC LIMIT $7 OFFSET $8`, q.User, q.GameType, q.Currency, q.Status, nullTime(q.From), nullTime(q.To), q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminBet{}
	for rows.Next() {
		var v AdminBet
		if err := rows.Scan(&v.BetID, &v.UserID, &v.LoginName, &v.GameType, &v.RoundSequence, &v.Currency, &v.Selection, &v.StakeMinor, &v.PayoutMinor, &v.Status, &v.CreatedAt, &v.SettledAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type LedgerRecord struct {
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
	rows, err := s.pool.Query(ctx, `SELECT x.id,u.public_id,COALESCE(u.login_name,''),x.currency,x.business_type,x.business_id,x.entry_type,x.amount_minor,x.balance_after_minor,x.remark,x.occurred_at FROM (SELECT le.id::text,w.user_id,w.currency,le.business_type,le.business_id,le.entry_type,le.amount_minor,le.balance_after_minor,''::text remark,le.occurred_at FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id UNION ALL SELECT p.id::text,p.user_id,'POINTS',p.business_type,p.business_id,p.business_type,p.amount_minor,p.balance_after_minor,p.remark,p.occurred_at FROM points_ledger p UNION ALL SELECT st.id::text,st.user_id,'STAMINA',st.business_type,st.business_id,st.business_type,st.amount_minor,st.balance_after_minor,st.remark,st.occurred_at FROM stamina_ledger st) x JOIN users u ON u.id=x.user_id WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') AND ($2='' OR x.currency=$2) AND ($3='' OR x.business_type=$3) AND ($4::timestamptz IS NULL OR x.occurred_at >= $4) AND ($5::timestamptz IS NULL OR x.occurred_at < $5) ORDER BY x.occurred_at DESC LIMIT $6 OFFSET $7`, q.User, q.Currency, q.BusinessType, nullTime(q.From), nullTime(q.To), q.Limit, q.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerRecord{}
	for rows.Next() {
		var v LedgerRecord
		if err := rows.Scan(&v.ID, &v.UserID, &v.LoginName, &v.Currency, &v.BusinessType, &v.BusinessID, &v.EntryType, &v.AmountMinor, &v.BalanceAfterMinor, &v.Remark, &v.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type RefundClearanceRecord struct {
	ID          string    `json:"id"`
	RecordType  string    `json:"record_type"`
	UserID      int64     `json:"user_id"`
	LoginName   string    `json:"login_name"`
	Currency    string    `json:"currency"`
	AmountMinor int64     `json:"amount_minor"`
	Status      string    `json:"status"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (s *Service) ListRefundClearances(ctx context.Context, user, status string, from, to time.Time, limit, offset int) ([]RefundClearanceRecord, error) {
	normalizePage(&limit, &offset)
	rows, err := s.pool.Query(ctx, `SELECT x.id,x.record_type,u.public_id,COALESCE(u.login_name,''),x.currency,x.amount_minor,x.status,x.occurred_at FROM (SELECT le.id::text,'bet_refund' record_type,w.user_id,w.currency,le.amount_minor,'completed' status,le.occurred_at FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id WHERE le.entry_type='refund' UNION ALL SELECT wd.id::text,'withdrawal',wd.user_id,w.currency,wd.amount_minor,wd.status,wd.created_at FROM withdrawals wd JOIN wallets w ON w.id=wd.wallet_id UNION ALL SELECT pw.id::text,'point_withdrawal',pw.user_id,'POINTS',pw.amount_minor,pw.status,pw.created_at FROM point_withdrawals pw) x JOIN users u ON u.id=x.user_id WHERE ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') AND ($2='' OR x.status=$2) AND ($3::timestamptz IS NULL OR x.occurred_at >= $3) AND ($4::timestamptz IS NULL OR x.occurred_at < $4) ORDER BY x.occurred_at DESC LIMIT $5 OFFSET $6`, user, status, nullTime(from), nullTime(to), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RefundClearanceRecord{}
	for rows.Next() {
		var v RefundClearanceRecord
		if err := rows.Scan(&v.ID, &v.RecordType, &v.UserID, &v.LoginName, &v.Currency, &v.AmountMinor, &v.Status, &v.OccurredAt); err != nil {
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
