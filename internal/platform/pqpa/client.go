package pqpa

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client implements the PQPA HTTPS API transport. Business services should
// depend on this small interface rather than on provider-specific payloads.
type Client struct {
	baseURL string
	apiKey  string
	secret  []byte
	http    *http.Client
	clock   func() time.Time
}

type CreateAddressRequest struct {
	UserID    string `json:"user_id"`
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
}

type Address struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
	Address   string `json:"address"`
}

type CreateWithdrawalRequest struct {
	ClientRequestID string `json:"client_request_id"`
	ChainCode       string `json:"chain_code"`
	TokenCode       string `json:"token_code"`
	Address         string `json:"address"`
	AmountMinor     int64  `json:"amount_minor"`
}

type Withdrawal struct {
	ID              string `json:"id"`
	ProviderOrderID string `json:"provider_order_id"`
	Status          string `json:"status"`
	TxHash          string `json:"tx_hash"`
}

type Chain struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

type Token struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Decimals int    `json:"decimals"`
	Active   bool   `json:"active"`
}

type ChainToken struct {
	ChainCode string `json:"chain_code"`
	TokenCode string `json:"token_code"`
	Active    bool   `json:"active"`
}

func NewClient(baseURL, apiKey, secret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, secret: []byte(secret), http: httpClient, clock: time.Now}
}

// Signature follows PQPA's four-header HMAC-SHA256 convention. The body is
// represented by its SHA-256 hex digest to keep signing independent of JSON
// formatting.
func (client *Client) Signature(method, path, timestamp, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	message := strings.Join([]string{strings.ToUpper(method), path, timestamp, nonce, hex.EncodeToString(bodyHash[:])}, "\n")
	mac := hmac.New(sha256.New, client.secret)
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func (client *Client) DoJSON(ctx context.Context, method, path string, requestBody any, responseBody any) error {
	var body []byte
	var err error
	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("marshal PQPA request: %w", err)
		}
	}
	timestamp := strconv.FormatInt(client.clock().UTC().UnixMilli(), 10)
	nonce := fmt.Sprintf("%d", client.clock().UTC().UnixNano())
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PQPA request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", client.apiKey)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", client.Signature(method, path, timestamp, nonce, body))
	resp, err := client.http.Do(req)
	if err != nil {
		return fmt.Errorf("call PQPA: %w", err)
	}
	defer resp.Body.Close()
	response, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("PQPA returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(response)))
	}
	if responseBody != nil && len(response) != 0 {
		if err := json.Unmarshal(response, responseBody); err != nil {
			return fmt.Errorf("decode PQPA response: %w", err)
		}
	}
	return nil
}

func (client *Client) CreateAddress(ctx context.Context, input CreateAddressRequest) (Address, error) {
	var output Address
	if err := client.DoJSON(ctx, http.MethodPost, "/v1/addresses", input, &output); err != nil {
		return Address{}, err
	}
	return output, nil
}

// CreateDepositAddress adapts PQPA's payload to the application-layer port.
func (client *Client) CreateDepositAddress(ctx context.Context, userID, chainCode, tokenCode string) (providerID, address string, err error) {
	result, err := client.CreateAddress(ctx, CreateAddressRequest{UserID: userID, ChainCode: chainCode, TokenCode: tokenCode})
	if err != nil {
		return "", "", err
	}
	return result.ID, result.Address, nil
}

func (client *Client) CreateWithdrawal(ctx context.Context, input CreateWithdrawalRequest) (Withdrawal, error) {
	var output Withdrawal
	if err := client.DoJSON(ctx, http.MethodPost, "/v1/withdrawals", input, &output); err != nil {
		return Withdrawal{}, err
	}
	return output, nil
}

func (client *Client) GetWithdrawal(ctx context.Context, providerOrderID string) (Withdrawal, error) {
	var output Withdrawal
	path := "/v1/withdrawals/" + providerOrderID
	if err := client.DoJSON(ctx, http.MethodGet, path, nil, &output); err != nil {
		return Withdrawal{}, err
	}
	return output, nil
}

func (client *Client) ListChains(ctx context.Context) ([]Chain, error) {
	var output []Chain
	if err := client.DoJSON(ctx, http.MethodGet, "/v1/support/chains", nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func (client *Client) ListTokens(ctx context.Context, chainCode string) ([]Token, error) {
	var output []Token
	path := "/v1/support/tokens?chain_code=" + url.QueryEscape(chainCode)
	if err := client.DoJSON(ctx, http.MethodGet, path, nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func (client *Client) ListChainTokens(ctx context.Context) ([]ChainToken, error) {
	var output []ChainToken
	if err := client.DoJSON(ctx, http.MethodGet, "/v1/support/chain-tokens", nil, &output); err != nil {
		return nil, err
	}
	return output, nil
}
