package chain

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// FindWithdrawal 按 ID 查询提现申请。
func (service *Service) FindWithdrawal(ctx context.Context, withdrawalID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := scanWithdrawal(service.pool.QueryRow(ctx, `SELECT `+withdrawalColumns+`
		FROM withdrawals
		JOIN wallets ON wallets.id = withdrawals.wallet_id
		WHERE withdrawals.id = $1`, withdrawalID), &withdrawal)
	if errors.Is(err, pgx.ErrNoRows) {
		return Withdrawal{}, ErrWithdrawalNotFound
	}
	if err != nil {
		return Withdrawal{}, err
	}
	return withdrawal, nil
}

func (service *Service) ListWithdrawals(ctx context.Context, status string, limit int) ([]Withdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT ` + withdrawalColumns + ` FROM withdrawals JOIN wallets ON wallets.id=withdrawals.wallet_id`
	args := []any{limit}
	if status != "" {
		query += ` WHERE withdrawals.status=$2`
	}
	query += ` ORDER BY withdrawals.created_at DESC LIMIT $1`
	if status != "" {
		args = append(args, status)
	}
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]Withdrawal, 0)
	for rows.Next() {
		var item Withdrawal
		if err := scanWithdrawal(rows, &item); err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	return output, rows.Err()
}

func (service *Service) ListUserWithdrawals(ctx context.Context, userID string, limit int) ([]Withdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `SELECT `+withdrawalColumns+`
		FROM withdrawals
		JOIN wallets ON wallets.id=withdrawals.wallet_id
		WHERE withdrawals.user_id=$1
		ORDER BY withdrawals.created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Withdrawal, 0)
	for rows.Next() {
		var item Withdrawal
		if err := scanWithdrawal(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func findWithdrawalByRequestID(ctx context.Context, tx pgx.Tx, userID string, clientRequestID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := scanWithdrawal(tx.QueryRow(ctx, `SELECT `+withdrawalColumns+`
		FROM withdrawals
		JOIN wallets ON wallets.id = withdrawals.wallet_id
		WHERE withdrawals.user_id = $1 AND withdrawals.client_request_id = $2`, userID, clientRequestID), &withdrawal)
	return withdrawal, err
}

func findWithdrawalForUpdate(ctx context.Context, tx pgx.Tx, withdrawalID string) (Withdrawal, error) {
	var withdrawal Withdrawal
	err := scanWithdrawal(tx.QueryRow(ctx, `SELECT `+withdrawalColumns+` FROM withdrawals JOIN wallets ON wallets.id=withdrawals.wallet_id WHERE withdrawals.id=$1 FOR UPDATE`, withdrawalID), &withdrawal)
	return withdrawal, err
}
