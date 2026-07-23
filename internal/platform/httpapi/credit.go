package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
)

// CreditService 定义管理员充值与体力消耗能力。
type CreditService interface {
	AdminCredit(ctx context.Context, input credit.AdminCreditInput) (credit.CreditResult, error)
	ConsumeStamina(ctx context.Context, input credit.ConsumeStaminaInput) (credit.ConsumeResult, error)
	Balances(ctx context.Context, userID string) ([]credit.BalanceInfo, error)
	ListPointsLedger(ctx context.Context, userID string, limit int, offset int) ([]credit.LedgerEntry, error)
	ListStaminaLedger(ctx context.Context, userID string, limit int, offset int) ([]credit.LedgerEntry, error)
	RequestPointWithdrawal(ctx context.Context, userID, requestID string, amount int64, remark string) (credit.PointWithdrawal, error)
	ReviewPointWithdrawal(ctx context.Context, id, reviewerID string, approved bool) error
	ListPointWithdrawals(ctx context.Context, userID, status string, limit int) ([]credit.PointWithdrawal, error)
}

func (server *Server) pointWithdrawals(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	userID := ""
	if claims, ok := ClaimsFromContext(request.Context()); ok {
		userID = claims.Subject
	}
	if userID == "" {
		userID = request.URL.Query().Get("account_id")
	}
	items, err := server.credits.ListPointWithdrawals(request.Context(), userID, request.URL.Query().Get("status"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list point withdrawals"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) adminPointWithdrawals(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	items, err := server.credits.ListPointWithdrawals(request.Context(), "", request.URL.Query().Get("status"), 50)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list point withdrawals"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}

func (server *Server) requestPointWithdrawal(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input struct {
		AccountID   string `json:"account_id"`
		RequestID   string `json:"request_id"`
		AmountMinor int64  `json:"amount_minor"`
		Remark      string `json:"remark"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	userID := input.AccountID
	if claims, ok := ClaimsFromContext(request.Context()); ok {
		userID = claims.Subject
	}
	result, err := server.credits.RequestPointWithdrawal(request.Context(), userID, input.RequestID, input.AmountMinor, input.Remark)
	switch {
	case errors.Is(err, credit.ErrInvalidAmount), errors.Is(err, credit.ErrUserNotFound):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, credit.ErrInsufficientStamina):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": "insufficient points balance"})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to request point withdrawal"})
	default:
		writeJSON(writer, http.StatusCreated, result)
	}
}

func (server *Server) reviewPointWithdrawal(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input struct {
		Approved bool `json:"approved"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20)).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	err := server.credits.ReviewPointWithdrawal(request.Context(), request.PathValue("withdrawalID"), claims.Subject, input.Approved)
	switch {
	case errors.Is(err, credit.ErrPointWithdrawalNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, credit.ErrPointWithdrawalState):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to review point withdrawal"})
	default:
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "point_withdrawal.review", TargetType: "point_withdrawal", TargetID: request.PathValue("withdrawalID"), Payload: map[string]any{"approved": input.Approved}})
		writeJSON(writer, http.StatusOK, map[string]string{"status": "processed"})
	}
}

// TaskService 定义每日签到能力。
type TaskService interface {
	Checkin(ctx context.Context, userID string) (task.CheckinResult, error)
}

func WithCredits(credits CreditService) Option {
	return func(server *Server) { server.credits = credits }
}

func WithTasks(tasks TaskService) Option {
	return func(server *Server) { server.tasks = tasks }
}

// adminCredit 处理管理员手动充值（积分/USDT/体力）。
func (server *Server) adminCredit(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input credit.AdminCreditInput
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if input.UserID == "" || input.Currency == "" || input.AmountMinor <= 0 || input.RequestID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "user_id, currency, amount_minor, request_id are required"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	input.OperatorID = claims.Subject

	result, err := server.credits.AdminCredit(request.Context(), input)
	switch {
	case errors.Is(err, credit.ErrInvalidAmount), errors.Is(err, credit.ErrInvalidCurrency):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, credit.ErrUserNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to process credit"})
		return
	}
	server.recordAudit(request.Context(), audit.Entry{
		ActorUserID: claims.Subject,
		Action:      "admin.credit",
		TargetType:  "user",
		TargetID:    input.UserID,
		Payload:     map[string]any{"currency": input.Currency, "amount_minor": input.AmountMinor, "credited": result.Credited},
	})
	writeJSON(writer, http.StatusOK, result)
}

// consumeStamina 处理参加活动消耗体力。
func (server *Server) consumeStamina(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	var input credit.ConsumeStaminaInput
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if input.UserID == "" || input.AmountMinor <= 0 || input.ActivityID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "user_id, amount_minor, activity_id are required"})
		return
	}
	if !authorizeAccount(request, input.UserID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cannot consume stamina for another account"})
		return
	}
	result, err := server.credits.ConsumeStamina(request.Context(), input)
	switch {
	case errors.Is(err, credit.ErrInvalidAmount):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, credit.ErrUserNotFound):
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	case errors.Is(err, credit.ErrInsufficientStamina):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to consume stamina"})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// checkin 处理每日签到。
func (server *Server) checkin(writer http.ResponseWriter, request *http.Request) {
	if server.tasks == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "task service is unavailable"})
		return
	}
	claims, ok := ClaimsFromContext(request.Context())
	if !ok || claims.Subject == "" {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "authentication is required"})
		return
	}
	result, err := server.tasks.Checkin(request.Context(), claims.Subject)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to check in"})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// allBalances 查询用户所有币种余额。
func (server *Server) allBalances(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	accountID := request.PathValue("accountID")
	if accountID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account ID is required"})
		return
	}
	if !authorizeAccount(request, accountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "wallet belongs to another account"})
		return
	}
	balances, err := server.credits.Balances(request.Context(), accountID)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read balances"})
		return
	}
	writeJSON(writer, http.StatusOK, balances)
}

// pointsLedger 查询用户积分流水。
func (server *Server) pointsLedger(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	accountID := request.PathValue("accountID")
	if accountID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account ID is required"})
		return
	}
	if !authorizeAccount(request, accountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "ledger belongs to another account"})
		return
	}
	limit, offset := parsePagination(request)
	entries, err := server.credits.ListPointsLedger(request.Context(), accountID, limit, offset)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read points ledger"})
		return
	}
	writeJSON(writer, http.StatusOK, entries)
}

// staminaLedger 查询用户体力流水。
func (server *Server) staminaLedger(writer http.ResponseWriter, request *http.Request) {
	if server.credits == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "credit service is unavailable"})
		return
	}
	accountID := request.PathValue("accountID")
	if accountID == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "account ID is required"})
		return
	}
	if !authorizeAccount(request, accountID) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "ledger belongs to another account"})
		return
	}
	limit, offset := parsePagination(request)
	entries, err := server.credits.ListStaminaLedger(request.Context(), accountID, limit, offset)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to read stamina ledger"})
		return
	}
	writeJSON(writer, http.StatusOK, entries)
}

func parsePagination(request *http.Request) (int, int) {
	limit := 50
	offset := 0
	if value := request.URL.Query().Get("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if value := request.URL.Query().Get("offset"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return limit, offset
}
