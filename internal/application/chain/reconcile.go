package chain

import (
	"context"
	"errors"
	"strings"
)

func (service *Service) ReconcileWithdrawals(ctx context.Context, limit int) (ReconcileResult, error) {
	if service.withdrawalProvider == nil {
		return ReconcileResult{}, errors.New("withdrawal provider is unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `SELECT id::text FROM withdrawals WHERE status='broadcasted' AND provider_order_id IS NOT NULL ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return ReconcileResult{}, err
	}
	orderIDs := make([]string, 0)
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			rows.Close()
			return ReconcileResult{}, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ReconcileResult{}, err
	}
	rows.Close()

	result := ReconcileResult{}
	for _, orderID := range orderIDs {
		status, txHash, reason, fee, err := service.withdrawalProvider.GetProviderWithdrawal(ctx, orderID)
		if err != nil {
			return result, err
		}
		result.Checked++
		terminalStatus := normalizeProviderWithdrawalStatus(status)
		if terminalStatus == "" {
			continue
		}
		if err := service.ApplyWithdrawalStatus(ctx, WithdrawalStatusInput{
			ProviderOrderID: orderID,
			TxHash:          txHash,
			Status:          terminalStatus,
			FailureReason:   reason,
			Fee:             fee,
		}); err != nil {
			return result, err
		}
		if terminalStatus == "confirmed" {
			result.Confirmed++
		} else {
			result.Failed++
		}
	}
	return result, nil
}

func normalizeProviderWithdrawalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "confirmed", "completed", "success", "succeeded":
		return "confirmed"
	case "failed", "rejected", "cancelled", "canceled":
		return "failed"
	default:
		return ""
	}
}
