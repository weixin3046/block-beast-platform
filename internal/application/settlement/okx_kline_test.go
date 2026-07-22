package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
)

func TestOkxKlineOutcome(t *testing.T) {
	// 目标分钟：2026-07-22 23:30:00 UTC
	targetMinute := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)
	targetTs := targetMinute.UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/market/candles" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		instId := r.URL.Query().Get("instId")
		if instId != "BTC-USDT" {
			t.Errorf("unexpected instId: %s", instId)
		}
		response := okxCandlesResponse{
			Code: "0",
			Data: [][]string{
				{fmt.Sprintf("%d", targetTs), "65000.00", "65100.00", "64900.00", "65032.17", "100.5", "6500000", "6500000", "1"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewOkxKlineResultSource(server.URL)
	round := game.Round{
		RoundID:     "r1",
		GameType:    "kline_btc_oddeven_194",
		Sequence:    1,
		BetClosesAt: targetMinute,
	}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{"symbol":"BTC-USDT"}`),
	}

	outcome, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	// 65032.17 尾数是 7 → odd
	if len(outcome) != 1 || outcome[0] != "odd" {
		t.Fatalf("outcome = %v, want [\"odd\"]", outcome)
	}
}

func TestOkxKlineOutcomeEven(t *testing.T) {
	targetMinute := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)
	targetTs := targetMinute.UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := okxCandlesResponse{
			Code: "0",
			Data: [][]string{
				{fmt.Sprintf("%d", targetTs), "65000.00", "65100.00", "64900.00", "65032.16", "100.5", "6500000", "6500000", "1"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewOkxKlineResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "kline_btc_oddeven_194", Sequence: 1, BetClosesAt: targetMinute}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{"symbol":"BTC-USDT"}`),
	}

	outcome, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("Outcome: %v", err)
	}
	// 65032.16 尾数是 6 → even
	if len(outcome) != 1 || outcome[0] != "even" {
		t.Fatalf("outcome = %v, want [\"even\"]", outcome)
	}
}

func TestOkxKlineNotReady(t *testing.T) {
	targetMinute := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := okxCandlesResponse{
			Code: "0",
			Data: [][]string{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewOkxKlineResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "kline_btc_oddeven_194", Sequence: 1, BetClosesAt: targetMinute}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{"symbol":"BTC-USDT"}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if !errors.Is(err, ErrKlineNotReady) {
		t.Fatalf("err = %v, want ErrKlineNotReady", err)
	}
}

func TestOkxKlineNotConfirmed(t *testing.T) {
	targetMinute := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)
	targetTs := targetMinute.UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := okxCandlesResponse{
			Code: "0",
			Data: [][]string{
				{fmt.Sprintf("%d", targetTs), "65000.00", "65100.00", "64900.00", "65032.17", "100.5", "6500000", "6500000", "0"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewOkxKlineResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "kline_btc_oddeven_194", Sequence: 1, BetClosesAt: targetMinute}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{"symbol":"BTC-USDT"}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if !errors.Is(err, ErrKlineNotReady) {
		t.Fatalf("err = %v, want ErrKlineNotReady", err)
	}
}

func TestOkxKlineTimestampMismatch(t *testing.T) {
	targetMinute := time.Date(2026, 7, 22, 23, 30, 0, 0, time.UTC)
	wrongTs := targetMinute.Add(-time.Minute).UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := okxCandlesResponse{
			Code: "0",
			Data: [][]string{
				{fmt.Sprintf("%d", wrongTs), "65000.00", "65100.00", "64900.00", "65032.17", "100.5", "6500000", "6500000", "1"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	source := NewOkxKlineResultSource(server.URL)
	round := game.Round{RoundID: "r1", GameType: "kline_btc_oddeven_194", Sequence: 1, BetClosesAt: targetMinute}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{"symbol":"BTC-USDT"}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if !errors.Is(err, ErrKlineNotReady) {
		t.Fatalf("err = %v, want ErrKlineNotReady", err)
	}
}

func TestOkxKlineMissingSymbol(t *testing.T) {
	source := NewOkxKlineResultSource("http://unused")
	round := game.Round{RoundID: "r1", GameType: "test", Sequence: 1, BetClosesAt: time.Now()}
	rules := game.Rules{
		Outcomes:         []string{"odd", "even"},
		PayoutMultiplier: 194,
		Source:           "okx_kline",
		Extras:           json.RawMessage(`{}`),
	}

	_, err := source.Outcome(context.Background(), round, rules)
	if err == nil {
		t.Fatal("should fail when symbol is missing")
	}
}
