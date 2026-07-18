package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/wallet"
)

func TestPlaceBetCreatesBet(t *testing.T) {
	placer := &recordingBetPlacer{bet: betting.PlacedBet{BetID: "bet-1", PlacedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), placer, readinessChecker{}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/bets", strings.NewReader(`{"ClientRequestID":"request-1","RoundID":"round-1","AccountID":"player-1","Currency":"USDT","Selection":{"color":"red"},"StakeMinor":2500}`))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if placer.request.ClientRequestID != "request-1" || placer.request.StakeMinor != 2500 {
		t.Fatalf("placer request = %#v", placer.request)
	}
	var body betting.PlacedBet
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.BetID != "bet-1" {
		t.Fatalf("bet ID = %q, want bet-1", body.BetID)
	}
}

func TestReadyReturnsServiceUnavailableWhenDependencyFails(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{err: errors.New("database unavailable")}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestBalanceReturnsWalletBalance(t *testing.T) {
	wallets := &recordingWalletReader{balance: wallet.AccountBalance{AccountID: "player-1", Currency: "USDT", AvailableMinor: 7_500, FrozenMinor: 250}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, wallets, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/wallets/player-1?currency=USDT", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if wallets.accountID != "player-1" || wallets.currency != "USDT" {
		t.Fatalf("wallet query = account %q, currency %q", wallets.accountID, wallets.currency)
	}
}

func TestRoundReturnsRound(t *testing.T) {
	rounds := &recordingRoundReader{round: game.Round{RoundID: "round-1", GameType: "dice", Status: game.RoundOpen}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds)
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds/round-1", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if rounds.roundID != "round-1" {
		t.Fatalf("round query = %q, want round-1", rounds.roundID)
	}
}

func TestOpenRoundsListsBoundedGameTypeRounds(t *testing.T) {
	rounds := &recordingRoundReader{rounds: []game.Round{{RoundID: "round-1", GameType: "dice", Status: game.RoundOpen}}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds)
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds?game_type=dice&limit=25", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if rounds.gameType != "dice" || rounds.limit != 25 {
		t.Fatalf("round query = game type %q, limit %d", rounds.gameType, rounds.limit)
	}
}

func TestOpenRoundsRejectsInvalidLimit(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, &recordingRoundReader{})
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds?game_type=dice&limit=101", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

type recordingBetPlacer struct {
	request betting.PlaceBetRequest
	bet     betting.PlacedBet
}

func (placer *recordingBetPlacer) PlaceBet(_ context.Context, request betting.PlaceBetRequest) (betting.PlacedBet, error) {
	placer.request = request
	return placer.bet, nil
}

type readinessChecker struct {
	err error
}

type recordingWalletReader struct {
	accountID string
	currency  string
	balance   wallet.AccountBalance
	err       error
}

func (reader *recordingWalletReader) Balance(_ context.Context, accountID string, currency string) (wallet.AccountBalance, error) {
	reader.accountID = accountID
	reader.currency = currency
	return reader.balance, reader.err
}

type recordingRoundReader struct {
	roundID  string
	round    game.Round
	rounds   []game.Round
	gameType string
	limit    int
	err      error
}

func (reader *recordingRoundReader) Find(_ context.Context, roundID string) (game.Round, error) {
	reader.roundID = roundID
	return reader.round, reader.err
}

func (reader *recordingRoundReader) ListOpen(_ context.Context, gameType string, limit int) ([]game.Round, error) {
	reader.gameType = gameType
	reader.limit = limit
	return reader.rounds, reader.err
}

func (checker readinessChecker) Ping(context.Context) error {
	return checker.err
}
