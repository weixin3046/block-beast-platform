package settlement

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// applyCommission pays the direct agent on every settled, non-refunded bet.
func applyCommission(ctx context.Context, tx pgx.Tx, betID, playerID, currency string, stake int64, settledAt interface{}) error {
	var agentID string
	err := tx.QueryRow(ctx, `SELECT parent_user_id::text FROM agent_relations WHERE user_id=$1 AND parent_user_id IS NOT NULL`, playerID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var rate int
	err = tx.QueryRow(ctx, `SELECT rate_basis_points FROM agent_commission_rates WHERE agent_user_id=$1`, agentID).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) || rate == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	amount := stake * int64(rate) / 10000
	if amount <= 0 {
		return nil
	}
	var commissionID string
	err = tx.QueryRow(ctx, `INSERT INTO commission_entries(id,source_bet_id,beneficiary_user_id,amount_minor,status) VALUES($1,$2,$3,$4,'paid') ON CONFLICT(source_bet_id,beneficiary_user_id) DO NOTHING RETURNING id`, uuid.NewString(), betID, agentID, amount).Scan(&commissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var walletID string
	var balance int64
	err = tx.QueryRow(ctx, `SELECT id,available_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, agentID, currency).Scan(&walletID, &balance)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,$3) RETURNING id,available_minor`, uuid.NewString(), agentID, currency).Scan(&walletID, &balance)
	}
	if err != nil {
		return err
	}
	balance += amount
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, balance); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES($1,$2,'commission',$3,'commission_credit',$4,$5)`, uuid.NewString(), walletID, commissionID, amount, balance)
	return err
}
