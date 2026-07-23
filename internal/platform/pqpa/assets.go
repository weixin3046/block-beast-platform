package pqpa

import (
	"context"

	"github.com/block-beast/platform/internal/application/pqpaassets"
)

// AssetProvider exposes PQPA metadata through the application-layer port.
type AssetProvider struct{ Client *Client }

func (provider AssetProvider) ListChainTokens(ctx context.Context) ([]pqpaassets.ChainToken, error) {
	items, err := provider.Client.ListChainTokens(ctx)
	if err != nil {
		return nil, err
	}
	output := make([]pqpaassets.ChainToken, 0, len(items))
	for _, item := range items {
		output = append(output, pqpaassets.ChainToken{ChainCode: item.ChainCode, TokenCode: item.TokenCode, Active: item.Active})
	}
	return output, nil
}
