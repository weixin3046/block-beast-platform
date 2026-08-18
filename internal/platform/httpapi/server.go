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
	config             config.Config
	logger             *slog.Logger
	betPlacer          BetPlacer
	readiness          ReadinessChecker
	wallets            WalletReader
	rounds             RoundReader
	bets               BetReader
	canceller          RoundCanceller
	auth               *Authenticator
	logins             LoginService
	registers          RegisterService
	sessions           SessionService
	passwords          PasswordChangeService
	secondaryPasswords SecondaryPasswordService
	auditor            AuditRecorder
	chainWebhook       *chainWebhookConfig
	withdrawals        WithdrawalService
	depositHistory     DepositReader
	depositAddresses   DepositAddressService
	credits            CreditService
	tasks              TaskService
	providerAssets     ProviderAssetReader
	agents             AgentService
	userAdmin          UserAdminService
	operations         OperationsService
	gameAdmin          GameAdminService
	gameRoomAdmin      GameRoomService
	chat               ChatService
	uploads            UploadService
	leaderboards       LeaderboardService
	redPackets         RedPacketService
	publicUsers        PublicUserResolver
}

type LoginService interface {
	Login(ctx context.Context, loginName string, password string) (auth.LoginResult, error)
	LoginAdmin(ctx context.Context, loginName string, password string) (auth.LoginResult, error)
}

type RegisterService interface {
	Register(ctx context.Context, loginName string, displayName string, password string, invitationCode string) (auth.LoginResult, error)
}

type SessionService interface {
	Refresh(ctx context.Context, refreshToken string) (auth.LoginResult, error)
	RefreshAdmin(ctx context.Context, refreshToken string) (auth.LoginResult, error)
	Logout(ctx context.Context, refreshToken string) error
}

type PasswordChangeService interface {
	ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error
}

type PublicUserResolver interface {
	InternalUserIDByPublicID(ctx context.Context, publicID int64) (string, error)
	PublicUserID(ctx context.Context, userID string) (int64, error)
}

type SecondaryPasswordService interface {
	SetSecondaryPassword(ctx context.Context, userID, ignored, secondaryPassword string) error
	VerifySecondaryPassword(ctx context.Context, userID, secondaryPassword string) error
	ChangeSecondaryPassword(ctx context.Context, userID, currentSecondaryPassword, newSecondaryPassword string) error
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

func WithSessions(sessions SessionService) Option {
	return func(server *Server) { server.sessions = sessions }
}

func WithPasswordChange(passwords PasswordChangeService) Option {
	return func(server *Server) { server.passwords = passwords }
}

func WithSecondaryPasswords(passwords SecondaryPasswordService) Option {
	return func(server *Server) { server.secondaryPasswords = passwords }
}

func WithPublicUserResolver(resolver PublicUserResolver) Option {
	return func(server *Server) { server.publicUsers = resolver }
}

func WithAudit(auditor AuditRecorder) Option {
	return func(server *Server) { server.auditor = auditor }
}

type BetPlacer interface {
	PlaceBet(ctx context.Context, request betting.PlaceBetRequest) (betting.PlacedBet, error)
}

type BetReader interface {
	Find(ctx context.Context, betID string) (betting.PlacedBet, error)
	ListUserBets(ctx context.Context, userID, status string, limit int) ([]betting.PlacedBet, error)
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
	State(ctx context.Context, gameType string) (game.RoundState, error)
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
	mux.HandleFunc("GET /v1/game-rooms", server.protect(server.gameRooms))
	mux.HandleFunc("GET /v1/announcements", server.announcements)
	mux.HandleFunc("GET /v1/configs/{key}", server.publicConfig)
	mux.HandleFunc("POST /v1/auth/login", server.login)
	mux.HandleFunc("POST /v1/admin/auth/login", server.adminLogin)
	mux.HandleFunc("POST /v1/auth/register", server.register)
	mux.HandleFunc("POST /v1/auth/refresh", server.refresh)
	mux.HandleFunc("POST /v1/admin/auth/refresh", server.adminRefresh)
	mux.HandleFunc("POST /v1/auth/logout", server.logout)
	mux.HandleFunc("GET /v1/users/me", server.protect(server.currentUser))
	mux.HandleFunc("GET /v1/avatars/{userID}", server.publicAvatar)
	mux.HandleFunc("PUT /v1/users/me", server.protect(server.updateCurrentProfile))
	mux.HandleFunc("PUT /v1/users/me/password", server.protect(server.changePassword))
	mux.HandleFunc("PUT /v1/users/me/secondary-password", server.protect(server.setSecondaryPassword))
	mux.HandleFunc("POST /v1/users/me/secondary-password/verify", server.protect(server.verifySecondaryPassword))
	mux.HandleFunc("POST /v1/chat/customer-service", server.protect(server.openCustomerServiceRoom))
	mux.HandleFunc("GET /v1/chat/rooms", server.protect(server.chatRooms))
	mux.HandleFunc("GET /v1/chat/rooms/{roomID}/messages", server.protect(server.chatMessages))
	mux.HandleFunc("POST /v1/chat/rooms/{roomID}/messages", server.protect(server.sendChatMessage))
	mux.HandleFunc("POST /v1/uploads/authorize", server.protect(server.authorizeUpload))
	mux.HandleFunc("POST /v1/uploads/{uploadID}/confirm", server.protect(server.confirmUpload))
	mux.HandleFunc("GET /v1/uploads/{uploadID}", server.protect(server.upload))
	mux.HandleFunc("PUT /v1/uploads/{uploadID}/content", server.protect(server.putUploadContent))
	mux.HandleFunc("GET /v1/uploads/{uploadID}/content", server.protect(server.downloadUploadContent))
	mux.HandleFunc("GET /v1/leaderboards/daily", server.protect(server.dailyLeaderboard))
	mux.HandleFunc("POST /v1/chat/rooms/{roomID}/red-packets", server.protect(server.createRedPacket))
	mux.HandleFunc("GET /v1/red-packets/{packetID}", server.protect(server.redPacket))
	mux.HandleFunc("POST /v1/red-packets/{packetID}/claim", server.protect(server.claimRedPacket))
	mux.HandleFunc("POST /v1/agents/bind", server.protect(server.bindAgent))
	mux.HandleFunc("GET /v1/agents/me", server.protect(server.agentRelation))
	mux.HandleFunc("GET /v1/agents/me/commissions", server.protect(server.commissions))
	mux.HandleFunc("GET /v1/agents/me/team-summary", server.protect(server.teamSummary))
	mux.HandleFunc("PUT /v1/admin/agents/{agentID}/commission-rate", server.protectRoles(server.setAgentCommissionRate, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/commissions", server.protectRoles(server.adminCommissions, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/commissions/{commissionID}/reverse", server.protectRoles(server.reverseCommission, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/agents/{agentID}/commissions", server.protectRoles(server.grantCommission, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/bets", server.protect(server.placeBet))
	mux.HandleFunc("GET /v1/bets/{betID}", server.protect(server.bet))
	mux.HandleFunc("GET /v1/bets", server.protect(server.userBets))
	mux.HandleFunc("GET /v1/wallets/{accountID}", server.protect(server.balance))
	mux.HandleFunc("GET /v1/rounds", server.protect(server.openRounds))
	mux.HandleFunc("GET /v1/rounds/state", server.protect(server.roundState))
	mux.HandleFunc("GET /v1/rounds/{roundID}", server.protect(server.round))
	mux.HandleFunc("POST /v1/rounds/{roundID}/cancel", server.protectRoles(server.cancelRound, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/webhooks/chain/deposits", server.chainDepositWebhook)
	mux.HandleFunc("POST /v1/webhooks/chain/withdrawals", server.chainWithdrawalWebhook)
	mux.HandleFunc("POST /v1/withdrawals", server.protect(server.requestWithdrawal))
	mux.HandleFunc("GET /v1/deposits", server.protect(server.userDeposits))
	mux.HandleFunc("GET /v1/withdrawals", server.protect(server.userWithdrawals))
	mux.HandleFunc("GET /v1/deposit-addresses", server.protect(server.depositAddress))
	mux.HandleFunc("POST /v1/deposit-addresses", server.protect(server.createDepositAddress))
	mux.HandleFunc("GET /v1/withdrawals/{withdrawalID}", server.protect(server.withdrawal))
	mux.HandleFunc("POST /v1/admin/withdrawals/{withdrawalID}/approve", server.protectRoles(server.approveWithdrawal, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/withdrawals", server.protectRoles(server.adminWithdrawals, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/withdrawals/{withdrawalID}/reject", server.protectRoles(server.rejectWithdrawal, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/credits", server.protectRoles(server.adminCredit, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/users", server.protectRoles(server.adminUsers, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/users/{userID}/status", server.protectRoles(server.setUserStatus, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/users/{userID}/agent-level", server.protectRoles(server.setAgentLevel, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/roles", server.protectRoles(server.adminRoles, identity.RoleAdmin))
	mux.HandleFunc("PUT /v1/admin/users/{userID}/roles", server.protectRoles(server.setUserRoles, identity.RoleAdmin))
	mux.HandleFunc("GET /v1/admin/announcements", server.protectRoles(server.adminAnnouncements, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/announcements", server.protectRoles(server.createAnnouncement, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/announcements/{announcementID}", server.protectRoles(server.updateAnnouncement, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/audit-logs", server.protectRoles(server.auditLogs, identity.RoleAdmin))
	mux.HandleFunc("GET /v1/admin/configs", server.protectRoles(server.adminConfigs, identity.RoleAdmin))
	mux.HandleFunc("PUT /v1/admin/configs/{key}", server.protectRoles(server.putConfig, identity.RoleAdmin))
	mux.HandleFunc("GET /v1/admin/tasks/bet-configs", server.protectRoles(server.adminBetTaskConfigs, identity.RoleAdmin))
	mux.HandleFunc("PUT /v1/admin/tasks/bet-configs", server.protectRoles(server.replaceBetTaskConfigs, identity.RoleAdmin))
	mux.HandleFunc("GET /v1/admin/game-types", server.protectRoles(server.adminGameTypes, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/game-rooms", server.protectRoles(server.adminGameRooms, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/game-rooms", server.protectRoles(server.createGameRoom, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/game-rooms/{roomID}", server.protectRoles(server.updateGameRoom, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/game-types", server.protectRoles(server.createGameType, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("PUT /v1/admin/game-types/{gameTypeID}", server.protectRoles(server.updateGameType, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/rounds", server.protectRoles(server.adminRounds, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/admin/rounds", server.protectRoles(server.createRound, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/point-withdrawals", server.protect(server.requestPointWithdrawal))
	mux.HandleFunc("GET /v1/point-withdrawals", server.protect(server.pointWithdrawals))
	mux.HandleFunc("POST /v1/admin/point-withdrawals/{withdrawalID}/review", server.protectRoles(server.reviewPointWithdrawal, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("GET /v1/admin/point-withdrawals", server.protectRoles(server.adminPointWithdrawals, identity.RoleAdmin, identity.RoleOperator))
	mux.HandleFunc("POST /v1/stamina/consume", server.protect(server.consumeStamina))
	mux.HandleFunc("POST /v1/tasks/checkin", server.protect(server.checkin))
	mux.HandleFunc("GET /v1/tasks/bet-progress", server.protect(server.betTasks))
	mux.HandleFunc("POST /v1/activities/lucky-spin", server.protect(server.luckySpin))
	mux.HandleFunc("GET /v1/wallets/{accountID}/all", server.protect(server.allBalances))
	mux.HandleFunc("GET /v1/points/{accountID}/ledger", server.protect(server.pointsLedger))
	mux.HandleFunc("GET /v1/stamina/{accountID}/ledger", server.protect(server.staminaLedger))
	return server.withCORS(server.withRequestLog(mux))
}

func (server *Server) withCORS(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(server.config.APIAllowedOrigins))
	allowAnyOrigin := false
	for _, origin := range server.config.APIAllowedOrigins {
		if origin == "*" {
			allowAnyOrigin = true
			continue
		}
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		_, originAllowed := allowed[origin]
		if allowAnyOrigin || originAllowed {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Add("Vary", "Origin")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
			writer.Header().Set("Access-Control-Max-Age", "600")
		}
		if request.Method == http.MethodOptions {
			if origin != "" {
				if !allowAnyOrigin && !originAllowed {
					writeJSON(writer, http.StatusForbidden, map[string]string{"error": "origin is not allowed"})
					return
				}
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
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
		LoginName      string `json:"login_name"`
		DisplayName    string `json:"display_name"`
		Password       string `json:"password"`
		InvitationCode string `json:"invitation_code"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.LoginName == "" || input.Password == "" || input.InvitationCode == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "login_name, password and invitation_code are required"})
		return
	}
	result, err := server.registers.Register(request.Context(), input.LoginName, input.DisplayName, input.Password, input.InvitationCode)
	switch {
	case errors.Is(err, auth.ErrInvalidLoginName), errors.Is(err, auth.ErrInvalidPassword), errors.Is(err, auth.ErrInvalidInvitationCode), errors.Is(err, identity.ErrInvitationCodeNotFound):
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
	server.loginForAudience(writer, request, false)
}

func (server *Server) adminLogin(writer http.ResponseWriter, request *http.Request) {
	server.loginForAudience(writer, request, true)
}

func (server *Server) loginForAudience(writer http.ResponseWriter, request *http.Request, admin bool) {
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
	var result auth.LoginResult
	var err error
	if admin {
		result, err = server.logins.LoginAdmin(request.Context(), input.LoginName, input.Password)
	} else {
		result, err = server.logins.Login(request.Context(), input.LoginName, input.Password)
	}
	auditAction := "auth.player_login"
	if admin {
		auditAction = "auth.admin_login"
	}
	switch {
	case errors.Is(err, auth.ErrTooManyLoginAttempts):
		server.recordAudit(request.Context(), audit.Entry{Action: auditAction, TargetType: "user", TargetID: input.LoginName, Payload: map[string]string{"outcome": "failure", "reason": "login_throttled"}})
		writer.Header().Set("Retry-After", "60")
		writeJSON(writer, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		server.recordAudit(request.Context(), audit.Entry{Action: auditAction, TargetType: "user", TargetID: input.LoginName, Payload: map[string]string{"outcome": "failure", "reason": "invalid_credentials"}})
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrAccountDisabled):
		server.recordAudit(request.Context(), audit.Entry{Action: auditAction, TargetType: "user", TargetID: input.LoginName, Payload: map[string]string{"outcome": "failure", "reason": "account_disabled"}})
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrAuthNotConfigured):
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to login"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{ActorUserID: result.UserID, Action: auditAction, TargetType: "user", TargetID: result.UserID, Payload: map[string]string{"outcome": "success"}})
	writeJSON(writer, http.StatusOK, result)
}

func (server *Server) refresh(writer http.ResponseWriter, request *http.Request) {
	server.refreshForAudience(writer, request, false)
}

func (server *Server) adminRefresh(writer http.ResponseWriter, request *http.Request) {
	server.refreshForAudience(writer, request, true)
}

func (server *Server) refreshForAudience(writer http.ResponseWriter, request *http.Request, admin bool) {
	if server.sessions == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "session refresh is unavailable"})
		return
	}
	token, ok := decodeRefreshToken(writer, request)
	if !ok {
		return
	}
	var result auth.LoginResult
	var err error
	if admin {
		result, err = server.sessions.RefreshAdmin(request.Context(), token)
	} else {
		result, err = server.sessions.Refresh(request.Context(), token)
	}
	switch {
	case errors.Is(err, auth.ErrInvalidRefreshToken):
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrAuthNotConfigured):
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to refresh session"})
	default:
		writeJSON(writer, http.StatusOK, result)
	}
}

func (server *Server) logout(writer http.ResponseWriter, request *http.Request) {
	if server.sessions == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "logout is unavailable"})
		return
	}
	token, ok := decodeRefreshToken(writer, request)
	if !ok {
		return
	}
	if err := server.sessions.Logout(request.Context(), token); err != nil {
		if errors.Is(err, auth.ErrInvalidRefreshToken) {
			writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to logout"})
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeRefreshToken(writer http.ResponseWriter, request *http.Request) (string, bool) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.RefreshToken == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
		return "", false
	}
	return input.RefreshToken, true
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
	server.writePublicJSON(writer, request, http.StatusOK, bet)
}

func (server *Server) userBets(writer http.ResponseWriter, request *http.Request) {
	if server.bets == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "bets are unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	userID := request.URL.Query().Get("account_id")
	if ok {
		userID = claims.Subject
	} else if userID != "" {
		internalID, err := server.resolvePublicUserID(request.Context(), userID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		userID = internalID
	}
	if userID == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	items, err := server.bets.ListUserBets(request.Context(), userID, request.URL.Query().Get("status"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list bets"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, items)
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

func (server *Server) roundState(writer http.ResponseWriter, request *http.Request) {
	if server.rounds == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "rounds are unavailable"})
		return
	}
	gameType := request.URL.Query().Get("game_type")
	if gameType == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "game type is required"})
		return
	}
	state, err := server.rounds.State(request.Context(), gameType)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to load round state"})
		return
	}
	// Return the server clock with the state so clients can compensate for local
	// device clock skew while rendering the betting countdown.
	writeJSON(writer, http.StatusOK, struct {
		game.RoundState
		ServerTime time.Time `json:"server_time"`
	}{
		RoundState: state,
		ServerTime: time.Now().UTC(),
	})
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
	internalID, err := server.resolvePublicUserID(request.Context(), accountID)
	if err != nil {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if !authorizeAccount(request, internalID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "wallet belongs to another account"})
		return
	}
	balance, err := server.wallets.Balance(request.Context(), internalID, currency)
	if errors.Is(err, wallet.ErrWalletNotFound) {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read wallet balance"})
		return
	}
	server.writePublicJSON(writer, request, http.StatusOK, balance)
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
	internalID, err := server.resolvePublicUserID(request.Context(), input.AccountID)
	if err != nil {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if !authorizeAccount(request, internalID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cannot place bets for another account"})
		return
	}
	input.AccountID = internalID

	bet, err := server.betPlacer.PlaceBet(request.Context(), input)
	if err != nil {
		writeBetError(writer, err)
		return
	}
	server.writePublicJSON(writer, request, http.StatusCreated, bet)
}

func writeBetError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, betting.ErrInvalidSelection), errors.Is(err, game.ErrInvalidStake):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, betting.ErrRoundNotFound), errors.Is(err, wallet.ErrWalletNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, game.ErrBettingClosed), errors.Is(err, wallet.ErrInsufficientFunds):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, betting.ErrAccountDisabled), errors.Is(err, betting.ErrBettingBanned):
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": err.Error()})
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
