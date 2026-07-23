package pqpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chainapp "github.com/block-beast/platform/internal/application/chain"
)

func TestCreateProviderWithdrawalUsesOfficialContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/wallet/withdraw/create" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		var input CreateWithdrawalRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.AppOrderNo != "w-1" || input.ChainCode != "POLYGON" ||
			input.ChainTokenID != 19 || input.Amount != "10.500000" ||
			input.ToAddress != "0xabc" {
			t.Fatalf("input = %+v", input)
		}
		_, _ = writer.Write([]byte(`{"code":0,"data":12345,"msg":""}`))
	}))
	defer server.Close()

	id, status, err := NewClient(server.URL, "key", "secret", server.Client()).
		CreateProviderWithdrawal(context.Background(), chainapp.ProviderWithdrawalRequest{
			RequestID: "w-1", ChainCode: "POLYGON", ChainTokenID: 19,
			Address: "0xabc", AmountMinor: 10_500_000, Decimals: 6,
		})
	if err != nil || id != "12345" || status != "accepted" {
		t.Fatalf("result = %q, %q, %v", id, status, err)
	}
}

func TestGetProviderWithdrawalUsesBusinessOrderNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/wallet/withdraw/get-by-business-id" ||
			request.URL.Query().Get("appOrderNo") != "w-1" {
			t.Fatalf("url = %s", request.URL.String())
		}
		_, _ = writer.Write([]byte(`{"code":0,"data":{"appOrderNo":"w-1","status":"SUCCESS","txHash":"0x1","fee":"0.1"},"msg":""}`))
	}))
	defer server.Close()

	status, txHash, reason, fee, err := NewClient(server.URL, "key", "secret", server.Client()).
		GetProviderWithdrawal(context.Background(), "w-1")
	if err != nil || status != "SUCCESS" || txHash != "0x1" || reason != "" || fee != "0.1" {
		t.Fatalf("result = %q, %q, %q, %q, %v", status, txHash, reason, fee, err)
	}
}

func TestCreateProviderWithdrawalRecoversAcceptedDuplicate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		switch request.URL.Path {
		case "/api/v1/wallet/withdraw/create":
			_, _ = writer.Write([]byte(`{"code":1007004000,"data":null,"msg":"duplicate appOrderNo"}`))
		case "/api/v1/wallet/withdraw/get-by-business-id":
			if request.URL.Query().Get("appOrderNo") != "w-duplicate" {
				t.Fatalf("appOrderNo = %q", request.URL.Query().Get("appOrderNo"))
			}
			_, _ = writer.Write([]byte(`{"code":0,"data":{"id":9876,"appOrderNo":"w-duplicate","status":"BROADCASTED"},"msg":""}`))
		default:
			t.Fatalf("path = %q", request.URL.Path)
		}
	}))
	defer server.Close()

	id, status, err := NewClient(server.URL, "key", "secret", server.Client()).
		CreateProviderWithdrawal(context.Background(), chainapp.ProviderWithdrawalRequest{
			RequestID: "w-duplicate", ChainCode: "POLYGON", ChainTokenID: 19,
			Address: "0xabc", AmountMinor: 1_000_000, Decimals: 6,
		})
	if err != nil || id != "9876" || status != "BROADCASTED" || requests != 2 {
		t.Fatalf("result = %q, %q, requests=%d, err=%v", id, status, requests, err)
	}
}
