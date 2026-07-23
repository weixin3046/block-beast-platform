package pqpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	chainapp "github.com/block-beast/platform/internal/application/chain"
)

type CreateWithdrawalRequest struct {
	AppOrderNo   string `json:"appOrderNo"`
	ChainCode    string `json:"chainCode"`
	ChainTokenID int64  `json:"chainTokenId"`
	ToAddress    string `json:"toAddress"`
	ToMemo       string `json:"toMemo,omitempty"`
	Amount       string `json:"amount"`
	CallbackURL  string `json:"callbackUrl,omitempty"`
	Remark       string `json:"remark,omitempty"`
}

type Withdrawal struct {
	ID            json.Number `json:"id"`
	AppOrderNo    string      `json:"appOrderNo"`
	Status        string      `json:"status"`
	TxHash        string      `json:"txHash"`
	FailureReason *string     `json:"failReason"`
	Fee           string      `json:"fee"`
}

func (client *Client) CreateWithdrawal(ctx context.Context, input CreateWithdrawalRequest) (json.Number, error) {
	var output json.Number
	if err := client.DoJSON(ctx, http.MethodPost, "/api/v1/wallet/withdraw/create", input, &output); err != nil {
		return "", err
	}
	return output, nil
}

func (client *Client) CreateProviderWithdrawal(ctx context.Context, input chainapp.ProviderWithdrawalRequest) (providerOrderID, status string, err error) {
	amount, err := FormatMinorAmount(input.AmountMinor, input.Decimals)
	if err != nil {
		return "", "", err
	}
	id, err := client.CreateWithdrawal(ctx, CreateWithdrawalRequest{
		AppOrderNo: input.RequestID, ChainCode: input.ChainCode, ChainTokenID: input.ChainTokenID,
		ToAddress: input.Address, ToMemo: input.Memo, Amount: amount, Remark: "平台用户提现",
	})
	if err != nil {
		var apiError *APIError
		if errors.As(err, &apiError) && apiError.Code == 1007004000 {
			existing, lookupErr := client.GetWithdrawal(ctx, input.RequestID)
			if lookupErr != nil {
				return "", "", errors.Join(err, lookupErr)
			}
			return existing.ID.String(), existing.Status, nil
		}
		return "", "", err
	}
	return id.String(), "accepted", nil
}

func (client *Client) GetWithdrawal(ctx context.Context, appOrderNo string) (Withdrawal, error) {
	var output Withdrawal
	path := "/api/v1/wallet/withdraw/get-by-business-id?appOrderNo=" + url.QueryEscape(appOrderNo)
	if err := client.DoJSON(ctx, http.MethodGet, path, nil, &output); err != nil {
		return Withdrawal{}, err
	}
	return output, nil
}

func (client *Client) GetProviderWithdrawal(ctx context.Context, appOrderNo string) (status, txHash, failureReason, fee string, err error) {
	result, err := client.GetWithdrawal(ctx, appOrderNo)
	if err != nil {
		return "", "", "", "", err
	}
	if result.FailureReason != nil {
		failureReason = *result.FailureReason
	}
	return result.Status, result.TxHash, failureReason, result.Fee, nil
}
