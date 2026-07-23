package pqpaassets

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChainToken struct {
	ChainCode string
	TokenCode string
	Active    bool
}

type Provider interface {
	ListChainTokens(ctx context.Context) ([]ChainToken, error)
}

type Service struct {
	pool     *pgxpool.Pool
	provider Provider
}

type Asset struct {
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
	TokenName string `json:"token_name"`
	Decimals  int    `json:"decimals"`
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
			INSERT INTO provider_supported_assets (id, provider, chain_code, token_code, enabled, synced_at)
			VALUES ($1, 'pqpa', $2, $3, $4, $5)
			ON CONFLICT (provider, chain_code, token_code) DO UPDATE SET enabled = EXCLUDED.enabled, synced_at = EXCLUDED.synced_at`, uuid.NewString(), asset.ChainCode, asset.TokenCode, asset.Active, time.Now().UTC()); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(assets), nil
}

func (service *Service) ListEnabled(ctx context.Context) ([]Asset, error) {
	rows, err := service.pool.Query(ctx, `SELECT chain_code, token_code, COALESCE(token_name, ''), decimals FROM provider_supported_assets WHERE provider='pqpa' AND enabled=true ORDER BY chain_code, token_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := make([]Asset, 0)
	for rows.Next() {
		var item Asset
		if err := rows.Scan(&item.ChainCode, &item.TokenCode, &item.TokenName, &item.Decimals); err != nil {
			return nil, err
		}
		assets = append(assets, item)
	}
	return assets, rows.Err()
}
