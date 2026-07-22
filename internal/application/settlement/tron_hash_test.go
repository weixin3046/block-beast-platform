package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
)

func TestTronHashOutcome(t *testing.T) {
	// 模拟 QuickNode JSON-RPC 返回区块哈希
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "eth_getBlockByNumber" {
			t.Errorf("unexpected method: %s", req.Method)
		}
		// 区块高度 84687810 = 84687805 + 1*5，hex = 0x50c3bc2
		if req.Params[0] != "0x50c3bc2" {
			t.Errorf("unexpected height param: %v", req.Params[0])
		}
		result := json.RawMessage(`{"hash":"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567895"}`)
		response := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: result}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewTronHashResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "tronhash_hash5_guess_194", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		Extras:           json.RawMessage(`{"base_block_height":84687805,"block_interval":5}`),
	}

	outcome, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	if len(outcome) != 1 || outcome[0] != "5" {
		t.Fatalf("outcome = %v, want [\"5\"]", outcome)
	}
}

func TestTronHashOutcomeBigSmall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := json.RawMessage(`{"hash":"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567895"}`)
		response := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: result}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewTronHashResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "tronhash_hash5_bigsmall_194", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"small", "big", "odd", "even"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		Extras:           json.RawMessage(`{"base_block_height":84687805,"block_interval":5}`),
	}

	outcome, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	// 尾数 5 → big + odd
	if len(outcome) != 2 || outcome[0] != "big" || outcome[1] != "odd" {
		t.Fatalf("outcome = %v, want [\"big\",\"odd\"]", outcome)
	}
}

func TestTronHashOutcomeDodge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := json.RawMessage(`{"hash":"0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567895"}`)
		response := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: result}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewTronHashResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "tronhash_hash5_dodge_194", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"dodge_0", "dodge_1", "dodge_2", "dodge_3", "dodge_4", "dodge_5", "dodge_6", "dodge_7", "dodge_8", "dodge_9"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		DodgeMode:        true,
		Extras:           json.RawMessage(`{"base_block_height":84687805,"block_interval":5}`),
	}

	outcome, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	if len(outcome) != 1 || outcome[0] != "dodge_5" {
		t.Fatalf("outcome = %v, want [\"dodge_5\"]", outcome)
	}
}

func TestTronHashBlockNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`null`)}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewTronHashResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "tronhash_hash5_guess_194", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		Extras:           json.RawMessage(`{"base_block_height":84687805,"block_interval":5}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("err = %v, want ErrBlockNotFound", err)
	}
}

func TestTronHashMissingExtras(t *testing.T) {
	source := NewTronHashResultSource("http://unused")
	round := game.Round{RoundID: "r1", GameType: "test", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"0", "1"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		Extras:           json.RawMessage(`{}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if err == nil {
		t.Fatal("should fail when base_block_height is missing")
	}
}

func TestTronHashRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "server error"},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewTronHashResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "test", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"0", "1"},
		PayoutMultiplier: 194,
		Source:           "tron_hash",
		Extras:           json.RawMessage(`{"base_block_height":84687805,"block_interval":5}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if err == nil || err.Error() != "tron rpc error -32000: server error" {
		t.Fatalf("err = %v, want tron rpc error", err)
	}
}
