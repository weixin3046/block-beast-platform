package chain

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type DepositAddress struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
	Address   string `json:"address"`
	Memo      string `json:"memo,omitempty"`
}

func (service *Service) GetDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (DepositAddress, error) {
	var output DepositAddress
	err := service.pool.QueryRow(ctx, `SELECT id, user_id, chain_code, token_code, address, COALESCE(memo, '') FROM chain_addresses WHERE user_id=$1 AND chain_code=$2 AND token_code=$3`, userID, chainCode, tokenCode).Scan(&output.ID, &output.UserID, &output.ChainCode, &output.TokenCode, &output.Address, &output.Memo)
	if errors.Is(err, pgx.ErrNoRows) {
		return DepositAddress{}, ErrDepositAddressNotFound
	}
	return output, err
}

func (service *Service) CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (DepositAddress, error) {
	if userID == "" || chainCode == "" || tokenCode == "" {
		return DepositAddress{}, ErrMissingFields
	}
	var enabled bool
	err := service.pool.QueryRow(ctx, `
		SELECT enabled AND support_deposit
		FROM provider_supported_assets
		WHERE provider='pqpa' AND chain_code=$1 AND token_code=$2`,
		chainCode, tokenCode).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) || !enabled {
		return DepositAddress{}, ErrUnsupportedAsset
	}
	if err != nil {
		return DepositAddress{}, err
	}
	if existing, err := service.GetDepositAddress(ctx, userID, chainCode, tokenCode); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrDepositAddressNotFound) {
		return DepositAddress{}, err
	}
	if service.addressProvider == nil {
		return DepositAddress{}, errors.New("deposit address provider is unavailable")
	}
	providerID, address, memo, err := service.addressProvider.CreateDepositAddress(ctx, userID, chainCode, tokenCode)
	if err != nil {
		return DepositAddress{}, err
	}
	output := DepositAddress{ID: uuid.NewString(), UserID: userID, ChainCode: chainCode, TokenCode: tokenCode, Address: address, Memo: memo}
	if err := service.pool.QueryRow(ctx, `
		INSERT INTO chain_addresses (id, user_id, chain_code, token_code, address, provider_address_id, memo)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		ON CONFLICT (chain_code, token_code, address) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id, user_id, chain_code, token_code, address, COALESCE(memo, '')`, output.ID, userID, chainCode, tokenCode, address, providerID, memo).
		Scan(&output.ID, &output.UserID, &output.ChainCode, &output.TokenCode, &output.Address, &output.Memo); err != nil {
		return DepositAddress{}, err
	}
	return output, nil
}
