package pqpa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSignatureIsDeterministic(t *testing.T) {
	client := NewClient("https://example.test", "key", "secret", nil)
	got := client.Signature("POST", "/v1/test", "1700000000000", "nonce", []byte(`{"amount":1}`))
	if got == "" || got != client.Signature("POST", "/v1/test", "1700000000000", "nonce", []byte(`{"amount":1}`)) {
		t.Fatalf("signature should be deterministic and non-empty: %q", got)
	}
}

func TestListChainTokensUsesPQPAEnvelopeAndFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/wallet/support/chain-tokens" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("X-Api-Key") != "key" || request.Header.Get("X-Signature") == "" {
			t.Fatal("missing PQPA authentication headers")
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code": 0,
			"msg":  "",
			"data": []map[string]any{{
				"chainCode": "POLYGON", "chainName": "Polygon", "tokenSymbol": "USDT",
				"chainTokenId": 19, "decimals": 6, "supportRecharge": true, "supportWithdraw": true,
			}},
		})
	}))
	defer server.Close()

	items, err := NewClient(server.URL, "key", "secret", server.Client()).ListChainTokens(context.Background())
	if err != nil {
		t.Fatalf("list chain tokens: %v", err)
	}
	if len(items) != 1 || items[0].ChainTokenID != 19 || items[0].ChainCode != "POLYGON" || items[0].TokenCode != "USDT" || items[0].Decimals != 6 || !items[0].SupportDeposit {
		t.Fatalf("items = %+v", items)
	}
}

func TestCreateDepositAddressUsesOfficialPQPAContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/wallet/address/create" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		var input map[string]any
		if err := json.Unmarshal(body, &input); err != nil {
			t.Fatal(err)
		}
		if input["chainCode"] != "POLYGON" || input["externalUserId"] != "user-1" {
			t.Fatalf("input = %v", input)
		}
		if _, exists := input["token_code"]; exists {
			t.Fatalf("unexpected legacy token_code: %v", input)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"code": 0,
			"msg":  "",
			"data": map[string]any{
				"id": 88001, "chainCode": "POLYGON", "externalUserId": "user-1", "address": "0xabc",
			},
		})
	}))
	defer server.Close()

	providerID, address, memo, err := NewClient(server.URL, "key", "secret", server.Client()).
		CreateDepositAddress(context.Background(), "user-1", "POLYGON", "USDT")
	if err != nil {
		t.Fatalf("create deposit address: %v", err)
	}
	if providerID != "88001" || address != "0xabc" || memo != "" {
		t.Fatalf("providerID = %q, address = %q, memo = %q", providerID, address, memo)
	}
}

func TestDoJSONReturnsPQPAApplicationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"code":401,"msg":"账号未登录","data":null}`))
	}))
	defer server.Close()

	err := NewClient(server.URL, "key", "secret", server.Client()).
		DoJSON(context.Background(), http.MethodGet, "/test", nil, &map[string]any{})
	if err == nil || err.Error() != "PQPA returned code 401: 账号未登录" {
		t.Fatalf("err = %v", err)
	}
}
