package pqpa

import (
	"context"
	"encoding/json"
	"net/http"
)

type CreateAddressRequest struct {
	ExternalUserID string `json:"externalUserId"`
	ChainCode      string `json:"chainCode"`
}

type Address struct {
	ID             json.Number `json:"id"`
	ExternalUserID string      `json:"externalUserId"`
	ChainCode      string      `json:"chainCode"`
	Address        string      `json:"address"`
	Memo           *string     `json:"memo"`
}

func (client *Client) CreateAddress(ctx context.Context, input CreateAddressRequest) (Address, error) {
	var output Address
	if err := client.DoJSON(ctx, http.MethodPost, "/api/v1/wallet/address/create", input, &output); err != nil {
		return Address{}, err
	}
	return output, nil
}

func (client *Client) CreateDepositAddress(ctx context.Context, userID, chainCode, _ string) (providerID, address, memo string, err error) {
	result, err := client.CreateAddress(ctx, CreateAddressRequest{ExternalUserID: userID, ChainCode: chainCode})
	if err != nil {
		return "", "", "", err
	}
	if result.Memo != nil {
		memo = *result.Memo
	}
	return result.ID.String(), result.Address, memo, nil
}
