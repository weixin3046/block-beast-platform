package pqpa

import (
	"context"
	"net/http"
	"net/url"
)

type Chain struct {
	Code   string `json:"chainCode"`
	Name   string `json:"chainName"`
	Active bool   `json:"enabled"`
}

type Token struct {
	ChainTokenID    int64  `json:"chainTokenId"`
	Code            string `json:"tokenSymbol"`
	Name            string `json:"tokenName"`
	Decimals        int    `json:"decimals"`
	SupportDeposit  bool   `json:"supportRecharge"`
	SupportWithdraw bool   `json:"supportWithdraw"`
}

type ChainToken struct {
	ChainTokenID    int64  `json:"chainTokenId"`
	ChainCode       string `json:"chainCode"`
	ChainName       string `json:"chainName"`
	TokenCode       string `json:"tokenSymbol"`
	Decimals        int    `json:"decimals"`
	SupportDeposit  bool   `json:"supportRecharge"`
	SupportWithdraw bool   `json:"supportWithdraw"`
}

func (client *Client) ListChains(ctx context.Context) ([]Chain, error) {
	var output []Chain
	if err := client.DoJSON(ctx, http.MethodGet, "/api/v1/wallet/support/chains", nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func (client *Client) ListTokens(ctx context.Context, chainCode string) ([]Token, error) {
	var output []Token
	path := "/api/v1/wallet/support/tokens?chainCode=" + url.QueryEscape(chainCode)
	if err := client.DoJSON(ctx, http.MethodGet, path, nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func (client *Client) ListChainTokens(ctx context.Context) ([]ChainToken, error) {
	var output []ChainToken
	if err := client.DoJSON(ctx, http.MethodGet, "/api/v1/wallet/support/chain-tokens", nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}
