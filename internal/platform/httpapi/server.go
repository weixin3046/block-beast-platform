package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/auth"
	"github.com/block-beast/platform/internal/application/betting"
	"github.com/block-beast/platform/internal/application/pqpaassets"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/game"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/block-beast/platform/internal/domain/wallet"
)

type Server struct {
	config           config.Config
	logger           *slog.Logger
	betPlacer        BetPlacer
	readiness        ReadinessChecker
	wallets          WalletReader
	rounds           RoundReader
	bets             BetReader
	canceller        RoundCanceller
	auth             *Authenticator
	logins           LoginService
	registers        RegisterService
	auditor          AuditRecorder
	chainWebhook     *chainWebhookConfig
	withdrawals      WithdrawalService
	depositAddresses DepositAddressService
	credits          CreditService
	tasks            TaskService
	providerAssets   ProviderAssetReader
}

type LoginService interface {
	Login(ctx context.Context, loginName string, password string) (auth.LoginResult, error)
}

type RegisterService interface {
	Register(ctx context.Context, loginName string, displayName string, password string) (auth.LoginResult, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, entry audit.Entry) error
}

// Option 按需装配服务器的可选能力（鉴权、登录、审计）。
type Option func(*Server)

type ProviderAssetReader interface {
	ListEnabled(ctx context.Context) ([]pqpaassets.Asset, error)
}

func WithProviderAssets(reader ProviderAssetReader) Option {
	return func(server *Server) { server.providerAssets = reader }
}

func WithAuth(authenticator *Authenticator) Option {
	return func(server *Server) { server.auth = authenticator }
}

func WithLogin(logins LoginService) Option {
	return func(server *Server) { server.logins = logins }
}

func WithRegister(registers RegisterService) Option {
	return func(server *Server) { server.registers = registers }
}

func WithAudit(auditor AuditRecorder) Option {
	return func(server *Server) { server.auditor = auditor }
}

type BetPlacer interface {
	PlaceBet(ctx context.Context, request betting.PlaceBetRequest) (betting.PlacedBet, error)
}

type BetReader interface {
	Find(ctx context.Context, betID string) (betting.PlacedBet, error)
}

type ReadinessChecker interface {
	Ping(ctx context.Context) error
}

type WalletReader interface {
	Balance(ctx context.Context, accountID string, currency string) (wallet.AccountBalance, error)
}

type RoundReader interface {
	Find(ctx context.Context, roundID string) (game.Round, error)
	ListOpen(ctx context.Context, gameType string, limit int) ([]game.Round, error)
}

type RoundCanceller interface {
	CancelRound(ctx context.Context, roundID string) (int, error)
}

func New(cfg config.Config, logger *slog.Logger, betPlacer BetPlacer, readiness ReadinessChecker, wallets WalletReader, rounds RoundReader, bets BetReader, canceller RoundCanceller, options ...Option) *Server {
	server := &Server{config: cfg, logger: logger, betPlacer: betPlacer, readiness: readiness, wallets: wallets, rounds: rounds, bets: bets, canceller: canceller}
	for _, option := range options {
		option(server)
	}
	return server
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /readyz", server.ready)
	mux.HandleFunc("GET /v1/platform", server.platform)
	mux.HandleFunc("GET /v1/assets", server.assets)
	mux.HandleFunc("POST /v1/auth/login", server.login)
	mux.HandleFunc("POST /v1/auth/register", server.register)
	mux.HandleFunc("POST /v1/bets", server.protect(server.placeBet))
	mux.HandleFunc("GET /v1/bets/{betID}", server.protect(server.bet))
	mux.HandleFunc("GET /v1/wallets/{accountID}", server.protect(server.balance))
	mux.HandleFunc("GET /v1/rounds", server.protect(server.openRounds))
	mux.HandleFunc("GET /v1/rounds/{roundID}", server.protect(server.round))
	mux.HandleFunc("POST /v1/rounds/{roundID}/cancel", server.protectRoles(server.cancelRound, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/webhooks/chain/deposits", server.chainDepositWebhook)
	mux.HandleFunc("POST /v1/webhooks/chain/withdrawals", server.chainWithdrawalWebhook)
	mux.HandleFunc("POST /v1/withdrawals", server.protect(server.requestWithdrawal))
	mux.HandleFunc("GET /v1/deposit-addresses", server.protect(server.depositAddress))
	mux.HandleFunc("POST /v1/deposit-addresses", server.protect(server.createDepositAddress))
	mux.HandleFunc("GET /v1/withdrawals/{withdrawalID}", server.protect(server.withdrawal))
	mux.HandleFunc("POST /v1/admin/withdrawals/{withdrawalID}/approve", server.protectRoles(server.approveWithdrawal, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/credits", server.protectRoles(server.adminCredit, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/point-withdrawals", server.protect(server.requestPointWithdrawal))
	mux.HandleFunc("POST /v1/admin/point-withdrawals/{withdrawalID}/review", server.protectRoles(server.reviewPointWithdrawal, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/stamina/consume", server.protect(server.consumeStamina))
	mux.HandleFunc("POST /v1/tasks/checkin", server.protect(server.checkin))
	mux.HandleFunc("GET /v1/wallets/{accountID}/all", server.protect(server.allBalances))
	mux.HandleFunc("GET /v1/points/{accountID}/ledger", server.protect(server.pointsLedger))
	mux.HandleFunc("GET /v1/stamina/{accountID}/ledger", server.protect(server.staminaLedger))
	return server.withRequestLog(mux)
}

// protect 在未配置鉴权时放行（保持本地开发兼容），否则要求有效令牌。
func (server *Server) protect(handler http.HandlerFunc) http.HandlerFunc {
	if server.auth == nil {
		return handler
	}
	return server.auth.Authenticate(handler)
}

func (server *Server) protectRoles(handler http.HandlerFunc, roles ...string) http.HandlerFunc {
	if server.auth == nil {
		return handler
	}
	return server.auth.RequireRoles(handler, roles...)
}

// recordAudit 追加审计记录；审计失败不阻断请求，仅记录警告。
func (server *Server) recordAudit(ctx context.Context, entry audit.Entry) {
	if server.auditor == nil {
		return
	}
	if err := server.auditor.Record(ctx, entry); err != nil {
		server.logger.Warn("audit record failed", "action", entry.Action, "target_id", entry.TargetID, "error", err)
	}
}

func (server *Server) register(writer http.ResponseWriter, request *http.Request) {
	if server.registers == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "registration is unavailable"})
		return
	}
	var input struct {
		LoginName   string `json:"login_name"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.LoginName == "" || input.Password == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "login_name and password are required"})
		return
	}
	result, err := server.registers.Register(request.Context(), input.LoginName, input.DisplayName, input.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidLoginName), errors.Is(err, auth.ErrInvalidPassword):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, identity.ErrLoginNameTaken):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrAuthNotConfigured):
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to register"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: result.UserID, Action: "auth.register", TargetType: "user", TargetID: result.UserID, Payload: map[string]string{"login_name": input.LoginName}})
	writeJSON(writer, http.StatusCreated, result)
}

func (server *Server) login(writer http.ResponseWriter, request *http.Request) {
	if server.logins == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "authentication is unavailable"})
		return
	}
	var input struct {
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.LoginName == "" || input.Password == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "login_name and password are required"})
		return
	}
	result, err := server.logins.Login(request.Context(), input.LoginName, input.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		server.recordAudit(request.Context(), audit.Entry{Action: "auth.login", TargetType: "user", TargetID: input.LoginName, Payload: map[string]string{"outcome": "failure", "reason": "invalid_credentials"}})
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrAccountDisabled):
		server.recordAudit(request.Context(), audit.Entry{Action: "auth.login", TargetType: "user", TargetID: input.LoginName, Payload: map[string]string{"outcome": "failure", "reason": "account_disabled"}})
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrAuthNotConfigured):
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to login"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: result.UserID, Action: "auth.login", TargetType: "user", TargetID: result.UserID, Payload: map[string]string{"outcome": "success"}})
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) cancelRound(writer http.ResponseWriter, request *http.Request) {
	if server.canceller == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "round cancellation is unavailable"})
		return
	}
	roundID := request.PathValue("roundID")
	refundedBetCount, err := server.canceller.CancelRound(request.Context(), roundID)
	if errors.Is(err, game.ErrRoundNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if errors.Is(err, game.ErrInvalidTransition) {
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to cancel round"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	server.recordAudit(request.Context(), audit.Entry{
		ActorUserID: claims.Subject,
		Action:      "round.cancel",
		TargetType:  "round",
		TargetID:    roundID,
		Payload:     map[string]int{"refunded_bet_count": refundedBetCount},
	})
	writeJSON(writer, http.StatusOK, struct {
		RoundID          string `json:"round_id"`
		RefundedBetCount int    `json:"refunded_bet_count"`
		Status           string `json:"status"`
	}{RoundID: roundID, RefundedBetCount: refundedBetCount, Status: "cancelled"})
}

func (server *Server) bet(writer http.ResponseWriter, request *http.Request) {
	if server.bets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "bets are unavailable"})
		return
	}
	bet, err := server.bets.Find(request.Context(), request.PathValue("betID"))
	if errors.Is(err, betting.ErrBetNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read bet"})
		return
	}
	if !authorizeAccount(request, bet.AccountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "bet belongs to another account"})
		return
	}
	writeJSON(writer, http.StatusOK, bet)
}

func (server *Server) openRounds(writer http.ResponseWriter, request *http.Request) {
	if server.rounds == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "rounds are unavailable"})
		return
	}
	gameType := request.URL.Query().Get("game_type")
	if gameType == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "game type is required"})
		return
	}
	limit := 50
	if value := request.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 || parsed > 100 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 100"})
			return
		}
		limit = parsed
	}
	rounds, err := server.rounds.ListOpen(request.Context(), gameType, limit)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list rounds"})
		return
	}
	writeJSON(writer, http.StatusOK, rounds)
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
	if !authorizeAccount(request, accountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "wallet belongs to another account"})
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
	if !authorizeAccount(request, input.AccountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cannot place bets for another account"})
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
