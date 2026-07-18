package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/wallet"
)

type Server struct {
	config    config.Config
	logger    *slog.Logger
	betPlacer BetPlacer
	readiness ReadinessChecker
	wallets   WalletReader
	rounds    RoundReader
}

type BetPlacer interface {
	PlaceBet(ctx context.Context, request betting.PlaceBetRequest) (betting.PlacedBet, error)
}

type ReadinessChecker interface {
	Ping(ctx context.Context) error
}

type WalletReader interface {
	Balance(ctx context.Context, accountID string, currency string) (wallet.AccountBalance, error)
}

type RoundReader interface {
	Find(ctx context.Context, roundID string) (game.Round, error)
}

func New(cfg config.Config, logger *slog.Logger, betPlacer BetPlacer, readiness ReadinessChecker, wallets WalletReader, rounds RoundReader) *Server {
	return &Server{config: cfg, logger: logger, betPlacer: betPlacer, readiness: readiness, wallets: wallets, rounds: rounds}
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("GET /v1/platform", server.platform)
	mux.HandleFunc("POST /v1/bets", server.placeBet)
	mux.HandleFunc("GET /v1/wallets/{accountID}", server.balance)
	mux.HandleFunc("GET /v1/rounds/{roundID}", server.round)
	return server.withRequestLog(mux)
}

func (server *Server) round(writer http.ResponseWriter, request *http.Request) {
	if server.rounds == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "rounds are unavailable"})
		return
	}
	round, err := server.rounds.Find(request.Context(), request.PathValue("roundID"))
	if errors.Is(err, game.ErrRoundNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read round"})
		return
	}
	writeJSON(writer, http.StatusOK, round)
}

func (server *Server) balance(writer http.ResponseWriter, request *http.Request) {
	if server.wallets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "wallets are unavailable"})
		return
	}
	accountID := request.PathValue("accountID")
	currency := request.URL.Query().Get("currency")
	if accountID == "" || currency == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account ID and currency are required"})
		return
	}
	balance, err := server.wallets.Balance(request.Context(), accountID, currency)
	if errors.Is(err, wallet.ErrWalletNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read wallet balance"})
		return
	}
	writeJSON(writer, http.StatusOK, balance)
}

func (server *Server) placeBet(writer http.ResponseWriter, request *http.Request) {
	if server.betPlacer == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "betting is unavailable"})
		return
	}
	var input betting.PlaceBetRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if input.ClientRequestID == "" || input.RoundID == "" || input.AccountID == "" || input.Currency == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "missing required bet fields"})
		return
	}

	bet, err := server.betPlacer.PlaceBet(request.Context(), input)
	if err != nil {
		writeBetError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, bet)
}

func writeBetError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, betting.ErrInvalidSelection), errors.Is(err, game.ErrInvalidStake):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, betting.ErrRoundNotFound), errors.Is(err, wallet.ErrWalletNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, game.ErrBettingClosed), errors.Is(err, wallet.ErrInsufficientFunds):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to place bet"})
	}
}

func (server *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (server *Server) ready(writer http.ResponseWriter, request *http.Request) {
	if server.readiness == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := server.readiness.Ping(ctx); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (server *Server) platform(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"environment": server.config.Environment,
		"domains":     []string{"identity", "wallet", "game", "agent", "realtime", "chain", "operations"},
	})
}

func (server *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		server.logger.Info("request completed", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
