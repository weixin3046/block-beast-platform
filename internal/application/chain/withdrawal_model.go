package chain

const withdrawalColumns = `
	withdrawals.id, withdrawals.user_id, withdrawals.client_request_id,
	withdrawals.destination_address, COALESCE(withdrawals.destination_memo, ''),
	COALESCE(withdrawals.chain_code, ''), wallets.currency, withdrawals.amount_minor,
	withdrawals.status, withdrawals.created_at, COALESCE(withdrawals.provider_order_id, ''),
	COALESCE(withdrawals.tx_hash, ''), COALESCE(withdrawals.failure_reason, '')`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWithdrawal(row rowScanner, output *Withdrawal) error {
	return row.Scan(
		&output.WithdrawalID, &output.UserID, &output.ClientRequestID,
		&output.DestinationAddress, &output.DestinationMemo, &output.ChainCode,
		&output.Currency, &output.AmountMinor, &output.Status, &output.CreatedAt,
		&output.ProviderOrderID, &output.TxHash, &output.FailureReason,
	)
}
