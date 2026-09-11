package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/operations"
)

type AnalyticsService interface {
	Monitor(ctx context.Context, userQuery, gameType string, limit int) (operations.Monitor, error)
	CurrentBets(ctx context.Context, userQuery, gameType string, limit int) ([]operations.MonitorBet, error)
	RoundCountdowns(ctx context.Context) ([]operations.MonitorRound, error)
	Dashboard(ctx context.Context, userQuery string, from, to time.Time, limit int) (operations.Dashboard, error)
	RecordLogin(ctx context.Context, userID, ip, audience string) error
	UserLoginIPs(ctx context.Context, publicID int64) ([]operations.LoginIP, error)
	UsersByLoginIP(ctx context.Context, ip string) ([]operations.LoginIPUser, error)
	CreateVirtualAccount(ctx context.Context, input operations.VirtualAccountInput) (operations.VirtualAccount, error)
	CreateVirtualAccounts(ctx context.Context, input operations.VirtualAccountInput) ([]operations.VirtualAccount, error)
	SetVirtualAutomation(ctx context.Context, publicID int64, input operations.VirtualAutomationInput) (operations.VirtualAccount, error)
	ListAdminBets(ctx context.Context, query operations.BetQuery) ([]operations.AdminBet, error)
	ListAdminLedger(ctx context.Context, query operations.LedgerQuery) ([]operations.LedgerRecord, error)
	ListRefundClearances(ctx context.Context, user, status string, from, to time.Time, limit, offset int) ([]operations.RefundClearanceRecord, error)
}

func WithAnalytics(service AnalyticsService) Option {
	return func(server *Server) { server.analytics = service }
}

func (server *Server) adminMonitor(w http.ResponseWriter, r *http.Request) {
	result, err := server.analytics.Monitor(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("game_type"), queryLimit(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load monitor"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) adminCurrentBets(w http.ResponseWriter, r *http.Request) {
	items, err := server.analytics.CurrentBets(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("game_type"), queryLimit(r, 100))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load current bets"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_time": time.Now().UTC(), "items": items})
}
func (server *Server) adminRoundCountdowns(w http.ResponseWriter, r *http.Request) {
	items, err := server.analytics.RoundCountdowns(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load round countdowns"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_time": time.Now().UTC(), "items": items})
}

func queryOffset(r *http.Request) int { v, _ := strconv.Atoi(r.URL.Query().Get("offset")); return v }
func reportTimes(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	from, e1 := parseTimeQuery(r.URL.Query().Get("from"))
	to, e2 := parseTimeQuery(r.URL.Query().Get("to"))
	if e1 != nil || e2 != nil || (!from.IsZero() && !to.IsZero() && !to.After(from)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid time range"})
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}
func (server *Server) adminBets(w http.ResponseWriter, r *http.Request) {
	playerType := r.URL.Query().Get("player_type")
	if playerType != "" && playerType != "all" && playerType != "real" && playerType != "virtual" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": operations.ErrInvalidPlayerType.Error()})
		return
	}
	from, to, ok := reportTimes(w, r)
	if !ok {
		return
	}
	items, err := server.analytics.ListAdminBets(r.Context(), operations.BetQuery{PlayerType: playerType, User: r.URL.Query().Get("user"), GameType: r.URL.Query().Get("game_type"), Currency: r.URL.Query().Get("currency"), Status: r.URL.Query().Get("status"), From: from, To: to, Limit: queryLimit(r, 50), Offset: queryOffset(r)})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to list bets"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (server *Server) adminLedger(w http.ResponseWriter, r *http.Request) {
	from, to, ok := reportTimes(w, r)
	if !ok {
		return
	}
	items, err := server.analytics.ListAdminLedger(r.Context(), operations.LedgerQuery{User: r.URL.Query().Get("user"), Currency: r.URL.Query().Get("currency"), BusinessType: r.URL.Query().Get("business_type"), From: from, To: to, Limit: queryLimit(r, 50), Offset: queryOffset(r)})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to list ledger"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (server *Server) adminRefundClearances(w http.ResponseWriter, r *http.Request) {
	from, to, ok := reportTimes(w, r)
	if !ok {
		return
	}
	items, err := server.analytics.ListRefundClearances(r.Context(), r.URL.Query().Get("user"), r.URL.Query().Get("status"), from, to, queryLimit(r, 50), queryOffset(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to list refunds and clearances"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func parseTimeQuery(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}
func (server *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	from, e1 := parseTimeQuery(r.URL.Query().Get("from"))
	to, e2 := parseTimeQuery(r.URL.Query().Get("to"))
	if e1 != nil || e2 != nil || (!from.IsZero() && !to.IsZero() && !to.After(from)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from and to must be RFC3339 and to must be after from"})
		return
	}
	result, err := server.analytics.Dashboard(r.Context(), r.URL.Query().Get("user"), from, to, queryLimit(r, 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load dashboard"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (server *Server) adminUserLoginIPs(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	items, err := server.analytics.UserLoginIPs(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to load login IPs"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (server *Server) adminLoginIPUsers(w http.ResponseWriter, r *http.Request) {
	items, err := server.analytics.UsersByLoginIP(r.Context(), r.PathValue("ip"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid IP address"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (server *Server) createVirtualAccount(w http.ResponseWriter, r *http.Request) {
	var in operations.VirtualAccountInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	in.ActorUserID = claims.Subject
	if in.Count <= 1 {
		v, err := server.analytics.CreateVirtualAccount(r.Context(), in)
		if errors.Is(err, operations.ErrInvalidVirtualAccount) || errors.Is(err, operations.ErrInvalidAvatar) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "unable to create virtual account"})
			return
		}
		server.recordAudit(r.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "virtual_account.create", TargetType: "user", TargetID: strconv.FormatInt(v.UserID, 10)})
		writeJSON(w, http.StatusCreated, map[string]any{"user_id": v.UserID, "login_name": v.LoginName, "display_name": v.DisplayName, "avatar_url": v.AvatarURL, "status": v.UserStatus, "is_virtual": true})
		return
	}
	accounts, err := server.analytics.CreateVirtualAccounts(r.Context(), in)
	if errors.Is(err, operations.ErrInvalidVirtualAccount) || errors.Is(err, operations.ErrInvalidAvatar) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unable to create virtual account"})
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, v := range accounts {
		server.recordAudit(r.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "virtual_account.create", TargetType: "user", TargetID: strconv.FormatInt(v.UserID, 10)})
		items = append(items, map[string]any{"user_id": v.UserID, "login_name": v.LoginName, "display_name": v.DisplayName, "avatar_url": v.AvatarURL, "status": v.UserStatus, "is_virtual": true})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": items})
}
func (server *Server) setVirtualAutomation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
		return
	}
	var in operations.VirtualAutomationInput
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	v, err := server.analytics.SetVirtualAutomation(r.Context(), id, in)
	if errors.Is(err, operations.ErrInvalidVirtualAccount) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if errors.Is(err, operations.ErrUserNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to update virtual automation"})
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	server.recordAudit(r.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "virtual_account.automation.update", TargetType: "user", TargetID: strconv.FormatInt(id, 10)})
	writeJSON(w, http.StatusOK, v)
}

func clientIP(r *http.Request) string {
	if value := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); value != "" {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
