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
)

func TestPlaceBetCreatesBet(t *testing.T) {
	placer := &recordingBetPlacer{bet: betting.PlacedBet{BetID: "bet-1", PlacedAt: time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), placer, readinessChecker{})
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
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{err: errors.New("database unavailable")})
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
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

func (checker readinessChecker) Ping(context.Context) error {
	return checker.err
}
