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
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), placer, readinessChecker{}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/bets", strings.NewReader(`{"client_request_id":"request-1","round_id":"round-1","account_id":100009,"currency":"USDT","selection":{"color":"red"},"stake_minor":2500}`))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"bet_id":"bet-1"`) {
		t.Fatalf("response body = %s, want snake_case bet ID", response.Body.String())
	}
	if placer.request.ClientRequestID != "request-1" || placer.request.AccountID != "100009" || placer.request.StakeMinor != 2500 {
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

func TestPlaceBetRejectsStringAccountID(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), &recordingBetPlacer{}, readinessChecker{}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/bets", strings.NewReader(`{"client_request_id":"request-1","round_id":"round-1","account_id":"100009","currency":"POINTS","selection":{"pick":"odd"},"stake_minor":1000}`))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "请求参数格式不正确") {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestCORSAllowsConfiguredOriginAndRejectsUnknownPreflight(t *testing.T) {
	server := New(config.Config{APIAllowedOrigins: []string{"https://player.example"}}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)

	request := httptest.NewRequest(http.MethodOptions, "/v1/bets", nil)
	request.Header.Set("Origin", "https://player.example")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://player.example" {
		t.Fatalf("allowed preflight = %d, origin %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}

	request = httptest.NewRequest(http.MethodOptions, "/v1/bets", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unknown origin status = %d, want 403", response.Code)
	}
}

func TestCORSAllowsAnyOriginWhenConfigured(t *testing.T) {
	server := New(config.Config{APIAllowedOrigins: []string{"*"}}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodOptions, "/v1/platform", nil)
	request.Header.Set("Origin", "http://frontend.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "http://frontend.example" {
		t.Fatalf("wildcard preflight = %d, origin %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestReadyReturnsServiceUnavailableWhenDependencyFails(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{err: errors.New("database unavailable")}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestBalanceReturnsWalletBalance(t *testing.T) {
	wallets := &recordingWalletReader{balance: wallet.AccountBalance{AccountID: "player-1", Currency: "USDT", AvailableMinor: 7_500, FrozenMinor: 250}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, wallets, nil, nil, nil)
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
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds, nil, nil)
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
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds, nil, nil)
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

func TestHashTrendsReturnsSharedResults(t *testing.T) {
	rounds := &recordingRoundReader{trend: game.HashTrend{GameType: "hash_29", Items: []game.HashTrendItem{{Sequence: 116, Digit: 5, Size: "big", Parity: "odd"}}}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/hash/trends?game_type=hash_29&limit=50", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || rounds.gameType != "hash_29" || rounds.limit != 50 {
		t.Fatalf("status=%d gameType=%q limit=%d body=%s", response.Code, rounds.gameType, rounds.limit, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"digit":5`) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestFixedHashAdminWriteRoutesAreNotExposed(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)
	for _, target := range []string{
		"/v1/admin/game-rooms",
		"/v1/admin/game-types",
		"/v1/admin/rounds",
	} {
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s status = %d, want 405", target, response.Code)
		}
	}
}

func TestOpenRoundsRejectsInvalidLimit(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, &recordingRoundReader{}, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds?game_type=dice&limit=101", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestRoundStateReturnsCurrentAndPrevious(t *testing.T) {
	current := game.Round{RoundID: "current", GameType: "play_hash", Status: game.RoundOpen}
	previous := game.Round{RoundID: "previous", GameType: "play_hash", Status: game.RoundSettled, Outcome: []string{"5"}}
	rounds := &recordingRoundReader{state: game.RoundState{Current: &current, Previous: &previous}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, rounds, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds/state?game_type=play_hash", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if rounds.gameType != "play_hash" {
		t.Fatalf("game type = %q, want play_hash", rounds.gameType)
	}
	if !strings.Contains(response.Body.String(), `"outcome":["5"]`) {
		t.Fatalf("response = %s, want previous outcome", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"server_time":`) {
		t.Fatalf("response = %s, want server_time", response.Body.String())
	}
}

func TestBetReturnsBet(t *testing.T) {
	bets := &recordingBetReader{bet: betting.PlacedBet{BetID: "bet-1", Status: "accepted"}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, bets, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/bets/bet-1", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if bets.betID != "bet-1" {
		t.Fatalf("bet query = %q, want bet-1", bets.betID)
	}
}

func TestPublicBetsSupportsPlayerTypeAndPagination(t *testing.T) {
	bets := &recordingBetReader{publicBets: []betting.PublicBet{{
		BetID:  "bet-public-1",
		Player: betting.PublicPlayer{UserID: 100009, DisplayName: "虚拟玩家", AvatarURL: "/v1/avatars/100009", IsVirtual: true},
		Status: "accepted",
	}}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, bets, nil, WithAuth(NewAuthenticator(testSecret)))
	request := httptest.NewRequest(http.MethodGet, "/v1/bets/public-feed?player_type=virtual&game_type=hash_9&currency=usdt&status=accepted&limit=20&offset=40", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{"player"}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if bets.publicQuery.PlayerType != "virtual" || bets.publicQuery.GameType != "hash_9" || bets.publicQuery.Currency != "usdt" || bets.publicQuery.Status != "accepted" || bets.publicQuery.Limit != 20 || bets.publicQuery.Offset != 40 {
		t.Fatalf("public query = %#v", bets.publicQuery)
	}
	if !strings.Contains(response.Body.String(), `"is_virtual":true`) || !strings.Contains(response.Body.String(), `"display_name":"虚拟玩家"`) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestPublicBetsRequiresAuthentication(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, &recordingBetReader{}, nil, WithAuth(NewAuthenticator(testSecret)))
	request := httptest.NewRequest(http.MethodGet, "/v1/bets/public-feed", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", response.Code, response.Body.String())
	}
}

func TestPublicBetsRejectsInvalidPlayerType(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, &recordingBetReader{}, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/bets/public-feed?player_type=robot", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestCancelRoundReturnsRefundedBetCount(t *testing.T) {
	canceller := &recordingRoundCanceller{refundedBetCount: 2}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, canceller)
	request := httptest.NewRequest(http.MethodPost, "/v1/rounds/round-1/cancel", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if canceller.roundID != "round-1" {
		t.Fatalf("cancelled round = %q, want round-1", canceller.roundID)
	}
	if !strings.Contains(response.Body.String(), `"refunded_bet_count":2`) {
		t.Fatalf("response body = %s, want refund count", response.Body.String())
	}
}

type recordingBetPlacer struct {
	request betting.PlaceBetRequest
	bet     betting.PlacedBet
}

type recordingBetReader struct {
	betID       string
	bet         betting.PlacedBet
	publicQuery betting.PublicBetQuery
	publicBets  []betting.PublicBet
	err         error
}

type recordingRoundCanceller struct {
	roundID          string
	refundedBetCount int
	err              error
}

func (canceller *recordingRoundCanceller) CancelRound(_ context.Context, roundID string) (int, error) {
	canceller.roundID = roundID
	return canceller.refundedBetCount, canceller.err
}

func (reader *recordingBetReader) Find(_ context.Context, betID string) (betting.PlacedBet, error) {
	reader.betID = betID
	return reader.bet, reader.err
}

func (reader *recordingBetReader) ListUserBets(_ context.Context, _ string, _ string, _, _ int) ([]betting.PlacedBet, error) {
	return nil, nil
}

func (reader *recordingBetReader) ListPublicBets(_ context.Context, query betting.PublicBetQuery) ([]betting.PublicBet, error) {
	reader.publicQuery = query
	return reader.publicBets, reader.err
}

func (reader *recordingBetReader) CancelBet(_ context.Context, betID, _ string) (betting.PlacedBet, error) {
	reader.betID = betID
	reader.bet.Status = "cancelled"
	return reader.bet, reader.err
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
	state    game.RoundState
	trend    game.HashTrend
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

func (reader *recordingRoundReader) State(_ context.Context, gameType string) (game.RoundState, error) {
	reader.gameType = gameType
	return reader.state, reader.err
}

func (reader *recordingRoundReader) HashTrend(_ context.Context, gameType string, limit int) (game.HashTrend, error) {
	reader.gameType = gameType
	reader.limit = limit
	return reader.trend, reader.err
}

func (checker readinessChecker) Ping(context.Context) error {
	return checker.err
}
