package chain

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

type PQPADepositWebhook struct {
	Event        string `json:"event"`
	ID           int64  `json:"id"`
	ChainTokenID int64  `json:"chainTokenId"`
	Address      string `json:"address"`
	Amount       string `json:"amount"`
	TxHash       string `json:"txHash"`
	TxStatus     string `json:"txStatus"`
}

type PQPAWithdrawalWebhook struct {
	Event        string `json:"event"`
	ID           int64  `json:"id"`
	AppOrderNo   string `json:"appOrderNo"`
	ChainTokenID int64  `json:"chainTokenId"`
	ToAddress    string `json:"toAddress"`
	Amount       string `json:"amount"`
	TxHash       string `json:"txHash"`
	Status       string `json:"status"`
}

func (service *Service) CreditPQPADeposit(ctx context.Context, callback PQPADepositWebhook) (DepositResult, bool, error) {
	if callback.Event != "recharge" || callback.ID <= 0 || callback.ChainTokenID <= 0 ||
		callback.Address == "" || callback.Amount == "" || callback.TxHash == "" || callback.TxStatus == "" {
		return DepositResult{}, false, ErrMissingFields
	}
	if callback.TxStatus != "SETTLED" {
		return DepositResult{}, false, nil
	}
	eventID := "recharge:" + strconv.FormatInt(callback.ID, 10)
	alreadyProcessed, err := service.beginProviderEvent(ctx, eventID, "recharge", callback)
	if err != nil {
		return DepositResult{}, false, err
	}
	if alreadyProcessed {
		return DepositResult{}, false, nil
	}
	var chainCode, tokenCode string
	var decimals int
	err = service.pool.QueryRow(ctx, `
		SELECT chain_code, token_code, decimals
		FROM provider_supported_assets
		WHERE provider='pqpa' AND provider_chain_token_id=$1 AND enabled=true AND support_deposit=true`,
		callback.ChainTokenID).Scan(&chainCode, &tokenCode, &decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		service.finishProviderEvent(ctx, eventID, ErrUnsupportedAsset)
		return DepositResult{}, false, ErrUnsupportedAsset
	}
	if err != nil {
		service.finishProviderEvent(ctx, eventID, err)
		return DepositResult{}, false, err
	}
	amountMinor, err := parseDecimalMinor(callback.Amount, decimals)
	if err != nil || amountMinor <= 0 {
		service.finishProviderEvent(ctx, eventID, ErrInvalidAmount)
		return DepositResult{}, false, ErrInvalidAmount
	}
	result, err := service.CreditDeposit(ctx, DepositInput{
		ProviderEventID: eventID,
		TxHash:          callback.TxHash, ChainCode: chainCode, TokenCode: tokenCode,
		Address: callback.Address, AmountMinor: amountMinor,
	})
	service.finishProviderEvent(ctx, eventID, err)
	return result, err == nil, err
}

func (service *Service) ApplyPQPAWithdrawal(ctx context.Context, callback PQPAWithdrawalWebhook) (bool, error) {
	if callback.Event != "withdraw" || callback.ID <= 0 || callback.AppOrderNo == "" || callback.Status == "" {
		return false, ErrMissingFields
	}
	status := normalizeProviderWithdrawalStatus(callback.Status)
	if status == "" {
		return false, nil
	}
	eventID := "withdraw:" + strconv.FormatInt(callback.ID, 10) + ":" + callback.Status
	alreadyProcessed, err := service.beginProviderEvent(ctx, eventID, "withdraw", callback)
	if err != nil || alreadyProcessed {
		return false, err
	}
	err = service.ApplyWithdrawalStatus(ctx, WithdrawalStatusInput{
		ProviderOrderID: callback.AppOrderNo,
		TxHash:          callback.TxHash,
		Status:          status,
	})
	service.finishProviderEvent(ctx, eventID, err)
	return true, err
}
