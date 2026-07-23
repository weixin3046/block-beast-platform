package pqpaassets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChainToken struct {
	ProviderChainTokenID int64
	ChainCode            string
	TokenCode            string
	TokenName            string
	Decimals             int
	SupportDeposit       bool
	SupportWithdraw      bool
}

type Provider interface {
	ListChainTokens(ctx context.Context) ([]ChainToken, error)
}

type Service struct {
	pool     *pgxpool.Pool
	provider Provider
}

type Asset struct {
	ChainCode       string `json:"chain_code"`
	TokenCode       string `json:"token_code"`
	TokenName       string `json:"token_name"`
	Decimals        int    `json:"decimals"`
	SupportDeposit  bool   `json:"support_deposit"`
	SupportWithdraw bool   `json:"support_withdraw"`
}

func NewService(pool *pgxpool.Pool, provider Provider) *Service {
	return &Service{pool: pool, provider: provider}
}

// Sync refreshes the provider cache atomically. A failed provider call leaves
// the previous successful cache untouched so payment options remain stable.
func (service *Service) Sync(ctx context.Context) (int, error) {
	assets, err := service.provider.ListChainTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("list PQPA chain tokens: %w", err)
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE provider_supported_assets SET enabled = false WHERE provider = 'pqpa'`); err != nil {
		return 0, err
	}
	for _, asset := range assets {
		if asset.ChainCode == "" || asset.TokenCode == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO provider_supported_assets (
				id, provider, provider_chain_token_id, chain_code, token_code,
				token_name, decimals, enabled, support_deposit, support_withdraw, synced_at
			)
			VALUES ($1, 'pqpa', $2, $3, $4, $5, $6, true, $7, $8, $9)
			ON CONFLICT (provider, chain_code, token_code) DO UPDATE SET
				provider_chain_token_id = EXCLUDED.provider_chain_token_id,
				token_name = EXCLUDED.token_name,
				decimals = EXCLUDED.decimals,
				enabled = true,
				support_deposit = EXCLUDED.support_deposit,
				support_withdraw = EXCLUDED.support_withdraw,
				synced_at = EXCLUDED.synced_at`,
			uuid.NewString(), asset.ProviderChainTokenID, asset.ChainCode, asset.TokenCode,
			asset.TokenName, asset.Decimals, asset.SupportDeposit, asset.SupportWithdraw, time.Now().UTC()); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(assets), nil
}

func (service *Service) ListEnabled(ctx context.Context) ([]Asset, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT chain_code, token_code, COALESCE(token_name, ''), decimals, support_deposit, support_withdraw
		FROM provider_supported_assets
		WHERE provider='pqpa' AND enabled=true
		ORDER BY chain_code, token_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]Asset, 0)
	for rows.Next() {
		var item Asset
		if err := rows.Scan(&item.ChainCode, &item.TokenCode, &item.TokenName, &item.Decimals, &item.SupportDeposit, &item.SupportWithdraw); err != nil {
			return nil, err
		}
		assets = append(assets, item)
	}
	return assets, rows.Err()
}
