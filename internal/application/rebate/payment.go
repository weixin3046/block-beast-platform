package rebate

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
)

var ErrBalanceOverflow = errors.New("返水金额超出余额范围")

// PayTx requires the caller to have prelocked all recipient wallets in the
// transaction's global wallet order. Commission classification is reused so the
// existing ledger/dashboard count the payment once, not twice.
func PayTx(ctx context.Context, tx pgx.Tx, betID, currency string, a Allocation) error {
	var virtual bool
	if e := tx.QueryRow(ctx, `SELECT is_virtual FROM users WHERE id=$1`, a.UserID).Scan(&virtual); e != nil {
		return e
	}
	if virtual {
		return nil
	}
	var id string
	e := tx.QueryRow(ctx, `INSERT INTO commission_entries(id,source_bet_id,beneficiary_user_id,currency,amount_minor,status) VALUES($1,$2,$3,$4,$5,'paid') ON CONFLICT(source_bet_id,beneficiary_user_id) DO NOTHING RETURNING id::text`, uuid.NewString(), betID, a.UserID, currency, a.AmountMinor).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var walletID string
	var balance int64
	if e = tx.QueryRow(ctx, `SELECT id::text,available_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, a.UserID, currency).Scan(&walletID, &balance); e != nil {
		return e
	}
	if a.AmountMinor <= 0 || balance > math.MaxInt64-a.AmountMinor {
		return ErrBalanceOverflow
	}
	balance += a.AmountMinor
	if _, e = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, balance); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'commission',$3,'commission_credit',$4,$5)`, uuid.NewString(), walletID, id, a.AmountMinor, balance); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO rebate_allocations(commission_id,bet_id,beneficiary_user_id,base_minor,agent_level,differential_per_mille) VALUES($1,$2,$3,$4,$5,$6)`, id, betID, a.UserID, a.BaseMinor, a.Level, a.DifferentialPerMille)
	return e
}
