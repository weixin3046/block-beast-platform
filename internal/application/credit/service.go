package credit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/block-beast/platform/internal/domain/wallet"
)

// 平台内支持的三种可充值币种。
const (
	CurrencyPoints             = "POINTS"
	CurrencyUSDT               = "USDT"
	CurrencyJade               = "JADE"
	CurrencyOriginStone        = "ORIGIN_STONE"
	CurrencyStamina            = "STAMINA"
	CurrencyUSDTStamina        = "USDT_STAMINA"
	CurrencyJadeStamina        = "JADE_STAMINA"
	CurrencyOriginStoneStamina = "ORIGIN_STONE_STAMINA"
)

func validCurrency(v string) bool {
	return strings.TrimSpace(v) != ""
}

// 业务类型常量，用于幂等键与流水查询。
const (
	BizAdminCredit     = "admin_credit"
	BizBetTaskReward   = "bet_task_reward"
	BizActivityConsume = "activity_consume"
)

var ErrInvalidAmount = errors.New("amount must be positive")
var ErrInvalidCurrency = errors.New("unsupported platform currency")
var ErrInsufficientBalance = errors.New("insufficient balance")
var ErrInsufficientStamina = ErrInsufficientBalance
var ErrUserNotFound = errors.New("user not found")
var ErrPointWithdrawalNotFound = errors.New("point withdrawal not found")
var ErrVirtualAccountWithdrawal = errors.New("virtual accounts cannot withdraw")
var ErrPointWithdrawalState = errors.New("point withdrawal cannot transition from its current status")

type Service struct {
	pool *pgxpool.Pool
}

type PointWithdrawal struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	ClientRequestID string    `json:"client_request_id"`
	AmountMinor     int64     `json:"amount_minor"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

func (service *Service) RequestPointWithdrawal(ctx context.Context, userID, requestID string, amount int64, remark string) (PointWithdrawal, error) {
	if userID == "" || requestID == "" {
		return PointWithdrawal{}, ErrUserNotFound
	}
	if amount <= 0 {
		return PointWithdrawal{}, ErrInvalidAmount
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PointWithdrawal{}, err
	}
	defer tx.Rollback(ctx)
	var existing PointWithdrawal
	err = tx.QueryRow(ctx, `SELECT id,user_id,client_request_id,amount_minor,status,created_at FROM point_withdrawals WHERE user_id=$1 AND client_request_id=$2`, userID, requestID).Scan(&existing.ID, &existing.UserID, &existing.ClientRequestID, &existing.AmountMinor, &existing.Status, &existing.CreatedAt)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PointWithdrawal{}, err
	}
	var isVirtual bool
	if err := tx.QueryRow(ctx, `SELECT is_virtual FROM users WHERE id=$1`, userID).Scan(&isVirtual); err != nil {
		return PointWithdrawal{}, err
	}
	if isVirtual {
		return PointWithdrawal{}, ErrVirtualAccountWithdrawal
	}
	var walletID string
	var available, frozen int64
	err = tx.QueryRow(ctx, `SELECT id,available_minor,frozen_minor FROM wallets WHERE user_id=$1 AND currency='POINTS' FOR UPDATE`, userID).Scan(&walletID, &available, &frozen)
	if errors.Is(err, pgx.ErrNoRows) {
		return PointWithdrawal{}, ErrUserNotFound
	}
	if err != nil {
		return PointWithdrawal{}, err
	}
	if available < amount {
		return PointWithdrawal{}, ErrInsufficientStamina
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,frozen_minor=$3,version=version+1,updated_at=now() WHERE id=$1`, walletID, available-amount, frozen+amount); err != nil {
		return PointWithdrawal{}, err
	}
	output := PointWithdrawal{ID: uuid.NewString(), UserID: userID, ClientRequestID: requestID, AmountMinor: amount, Status: "requested"}
	err = tx.QueryRow(ctx, `INSERT INTO point_withdrawals(id,user_id,wallet_id,client_request_id,amount_minor,status,remark) VALUES($1,$2,$3,$4,$5,'requested',$6) RETURNING created_at`, output.ID, userID, walletID, requestID, amount, remark).Scan(&output.CreatedAt)
	if err != nil {
		return PointWithdrawal{}, err
	}
	if err := writeLedger(ctx, tx, userID, CurrencyPoints, "point_withdrawal", output.ID, -amount, available-amount, remark, ""); err != nil {
		return PointWithdrawal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PointWithdrawal{}, err
	}
	return output, nil
}

func (service *Service) ReviewPointWithdrawal(ctx context.Context, id, reviewerID string, approved bool) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var walletID, status, userID string
	var amount, available, frozen int64
	err = tx.QueryRow(ctx, `SELECT p.wallet_id,p.status,p.user_id,p.amount_minor,w.available_minor,w.frozen_minor FROM point_withdrawals p JOIN wallets w ON w.id=p.wallet_id WHERE p.id=$1 FOR UPDATE`, id).Scan(&walletID, &status, &userID, &amount, &available, &frozen)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPointWithdrawalNotFound
	}
	if err != nil {
		return err
	}
	if status != "requested" || frozen < amount {
		return ErrPointWithdrawalState
	}
	newStatus := "approved"
	balanceAfter := available
	entryType := "point_withdrawal_debit"
	entryAmount := -amount
	if !approved {
		newStatus = "rejected"
		balanceAfter = available + amount
		entryType = "point_withdrawal_unfreeze"
		entryAmount = amount
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,frozen_minor=$3,version=version+1,updated_at=now() WHERE id=$1`, walletID, balanceAfter, frozen-amount); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE point_withdrawals SET status=$2,reviewed_by=$3,reviewed_at=now() WHERE id=$1`, id, newStatus, reviewerID); err != nil {
		return err
	}
	if err := writeLedger(ctx, tx, userID, CurrencyPoints, entryType, id, entryAmount, balanceAfter, "", reviewerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (service *Service) ListPointWithdrawals(ctx context.Context, userID, status string, limit int) ([]PointWithdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id,user_id,client_request_id,amount_minor,status,created_at FROM point_withdrawals WHERE 1=1`
	args := []any{}
	index := 1
	if userID != "" {
		query += fmt.Sprintf(" AND user_id=$%d", index)
		args = append(args, userID)
		index++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status=$%d", index)
		args = append(args, status)
		index++
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", index)
	args = append(args, limit)
	rows, err := service.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]PointWithdrawal, 0)
	for rows.Next() {
		var item PointWithdrawal
		if err := rows.Scan(&item.ID, &item.UserID, &item.ClientRequestID, &item.AmountMinor, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		output = append(output, item)
	}
	return output, rows.Err()
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// AdminCreditInput 是管理员手动充值的请求。
type AdminCreditInput struct {
	UserID      string `json:"user_id"`
	Currency    string `json:"currency"`
	Amount      string `json:"amount"` // 展示单位十进制字符串，由后端换算为最小单位
	AmountMinor int64  `json:"-"`
	Remark      string `json:"remark"`
	OperatorID  string `json:"-"`          // 从访问令牌注入，不信任请求体
	RequestID   string `json:"request_id"` // 幂等键，同一 request_id 不重复入账
}

type CreditResult struct {
	UserID            string    `json:"user_id"`
	Currency          string    `json:"currency"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Credited          bool      `json:"credited"` // false 表示重复请求未重复入账
	OccurredAt        time.Time `json:"occurred_at"`
}

// AdminCredit 保留原上分入口，资金处理统一进入 AdjustWallet。
func (service *Service) AdminCredit(ctx context.Context, input AdminCreditInput) (CreditResult, error) {
	// 保持原接口的基础参数错误语义。
	if input.UserID == "" || input.RequestID == "" {
		return CreditResult{}, ErrUserNotFound
	}
	if !validCurrency(input.Currency) {
		return CreditResult{}, ErrInvalidCurrency
	}
	if !wallet.ValidDisplayAmount(input.Amount) {
		return CreditResult{}, ErrInvalidAmount
	}
	result, err := service.AdjustWallet(ctx, AdjustmentInput{UserID: input.UserID, Currency: input.Currency, Action: "credit", Amount: input.Amount, Remark: input.Remark, RequestID: input.RequestID, OperatorID: input.OperatorID})
	return CreditResult{UserID: result.UserID, Currency: result.Currency, AmountMinor: result.AmountMinor, BalanceAfterMinor: result.BalanceAfterMinor, Credited: !result.Duplicate, OccurredAt: result.OccurredAt}, err
}

// ConsumeStaminaInput 是参加活动消耗体力的请求。
type ConsumeStaminaInput struct {
	UserID      string `json:"user_id"`
	AmountMinor int64  `json:"amount_minor"`
	ActivityID  string `json:"activity_id"` // 活动标识，作为幂等键一部分
	Remark      string `json:"remark"`
}

type ConsumeResult struct {
	UserID            string    `json:"user_id"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Consumed          bool      `json:"consumed"` // false 表示重复请求未重复扣减
	OccurredAt        time.Time `json:"occurred_at"`
}

// ConsumeStamina 幂等扣减体力：同一 (user_id, activity_id) 不重复扣减。
func (service *Service) ConsumeStamina(ctx context.Context, input ConsumeStaminaInput) (ConsumeResult, error) {
	if input.AmountMinor <= 0 {
		return ConsumeResult{}, ErrInvalidAmount
	}
	if input.UserID == "" || input.ActivityID == "" {
		return ConsumeResult{}, ErrUserNotFound
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ConsumeResult{}, err
	}
	defer tx.Rollback(ctx)

	bizID := "consume:" + input.ActivityID
	if existing, err := findStaminaLedger(ctx, tx, input.UserID, BizActivityConsume, bizID); err == nil {
		return ConsumeResult{
			UserID:            input.UserID,
			AmountMinor:       -existing.AmountMinor,
			BalanceAfterMinor: existing.BalanceAfterMinor,
			Consumed:          false,
			OccurredAt:        existing.OccurredAt,
		}, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ConsumeResult{}, err
	}

	balanceAfter, err := deductBalance(ctx, tx, input.UserID, CurrencyStamina, input.AmountMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConsumeResult{}, ErrUserNotFound
	}
	if err != nil {
		return ConsumeResult{}, err
	}

	if err := writeLedger(ctx, tx, input.UserID, CurrencyStamina, BizActivityConsume, bizID, -input.AmountMinor, balanceAfter, input.Remark, ""); err != nil {
		return ConsumeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConsumeResult{}, err
	}
	return ConsumeResult{
		UserID:            input.UserID,
		AmountMinor:       input.AmountMinor,
		BalanceAfterMinor: balanceAfter,
		Consumed:          true,
		OccurredAt:        time.Now().UTC(),
	}, nil
}

// RewardStamina 发放活动任务体力奖励，供 task service 调用。
// 在同一事务中更新 wallets 余额并写 ledger_entries；bizType 区分奖励来源。
func (service *Service) RewardStamina(ctx context.Context, tx pgx.Tx, userID string, bizType string, bizID string, amountMinor int64, remark string) (int64, error) {
	return service.RewardCurrency(ctx, tx, userID, CurrencyStamina, bizType, bizID, amountMinor, remark)
}

// RewardCurrency 在当前事务内向指定币种发奖并写对应流水。
func (service *Service) RewardCurrency(ctx context.Context, tx pgx.Tx, userID, currency, bizType, bizID string, amountMinor int64, remark string) (int64, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if !validCurrency(currency) {
		return 0, ErrInvalidCurrency
	}
	if amountMinor <= 0 {
		return 0, ErrInvalidAmount
	}
	balanceAfter, err := addBalance(ctx, tx, userID, currency, amountMinor)
	if err != nil {
		return 0, err
	}
	if err := writeLedger(ctx, tx, userID, currency, bizType, bizID, amountMinor, balanceAfter, remark, ""); err != nil {
		return 0, err
	}
	return balanceAfter, nil
}

// Balance 查询用户指定币种余额。
func (service *Service) Balance(ctx context.Context, userID string, currency string) (BalanceInfo, error) {
	var info BalanceInfo
	err := service.pool.QueryRow(ctx, `
		SELECT w.user_id, w.currency, w.available_minor, w.frozen_minor,c.decimals
		FROM wallets w JOIN currencies c ON c.code=w.currency WHERE w.user_id = $1 AND w.currency = $2`, userID, currency).
		Scan(&info.UserID, &info.Currency, &info.AvailableMinor, &info.FrozenMinor, &info.Decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return BalanceInfo{}, ErrUserNotFound
	}
	if err != nil {
		return BalanceInfo{}, err
	}
	info.Available, _ = wallet.FormatDisplayAmount(info.AvailableMinor, info.Decimals)
	info.Frozen, _ = wallet.FormatDisplayAmount(info.FrozenMinor, info.Decimals)
	return info, nil
}

// Balances 查询用户所有币种余额。
func (service *Service) Balances(ctx context.Context, userID string) ([]BalanceInfo, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT w.user_id, w.currency, w.available_minor, w.frozen_minor,c.decimals
		FROM wallets w JOIN currencies c ON c.code=w.currency WHERE w.user_id = $1 ORDER BY w.currency`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	infos := make([]BalanceInfo, 0)
	for rows.Next() {
		var info BalanceInfo
		if err := rows.Scan(&info.UserID, &info.Currency, &info.AvailableMinor, &info.FrozenMinor, &info.Decimals); err != nil {
			return nil, err
		}
		info.Available, _ = wallet.FormatDisplayAmount(info.AvailableMinor, info.Decimals)
		info.Frozen, _ = wallet.FormatDisplayAmount(info.FrozenMinor, info.Decimals)
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

type BalanceInfo struct {
	UserID         string `json:"user_id"`
	Currency       string `json:"currency"`
	AvailableMinor int64  `json:"available_minor"`
	FrozenMinor    int64  `json:"frozen_minor"`
	Decimals       int    `json:"decimals"`
	Available      string `json:"available"`
	Frozen         string `json:"frozen"`
}

// LedgerEntry 是一条积分或体力流水。
type LedgerEntry struct {
	ID                string    `json:"id"`
	UserID            string    `json:"user_id"`
	BusinessType      string    `json:"business_type"`
	BusinessID        string    `json:"business_id"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Remark            string    `json:"remark"`
	OperatorID        string    `json:"operator_id,omitempty"`
	OccurredAt        time.Time `json:"occurred_at"`
}

// ListPointsLedger 分页查询用户积分流水（按时间倒序）。
func (service *Service) ListPointsLedger(ctx context.Context, userID string, limit int, offset int) ([]LedgerEntry, error) {
	return listLedger(ctx, service.pool, CurrencyPoints, userID, limit, offset)
}

// ListStaminaLedger 分页查询用户体力流水（按时间倒序）。
func (service *Service) ListStaminaLedger(ctx context.Context, userID string, limit int, offset int) ([]LedgerEntry, error) {
	return listLedger(ctx, service.pool, CurrencyStamina, userID, limit, offset)
}

func listLedger(ctx context.Context, pool *pgxpool.Pool, currency string, userID string, limit int, offset int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT le.id,w.user_id,le.business_type,le.business_id,le.amount_minor,le.balance_after_minor,le.remark,COALESCE(le.operator_id::text,''),le.occurred_at
        FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id WHERE w.user_id=$1 AND w.currency=$4 ORDER BY le.occurred_at DESC,le.id DESC LIMIT $2 OFFSET $3`
	rows, err := pool.Query(ctx, query, userID, limit, offset, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]LedgerEntry, 0)
	for rows.Next() {
		var entry LedgerEntry
		if err := rows.Scan(&entry.ID, &entry.UserID, &entry.BusinessType, &entry.BusinessID, &entry.AmountMinor, &entry.BalanceAfterMinor, &entry.Remark, &entry.OperatorID, &entry.OccurredAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// addBalance 锁钱包并增加余额，钱包不存在时创建。返回变更后余额。
func addBalance(ctx context.Context, tx pgx.Tx, userID string, currency string, delta int64) (int64, error) {
	walletID := uuid.NewString()
	_, err := tx.Exec(ctx, `
		INSERT INTO wallets (id, user_id, currency) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, currency) DO NOTHING`, walletID, userID, currency)
	if err != nil {
		return 0, err
	}
	var balanceAfter int64
	err = tx.QueryRow(ctx, `
		UPDATE wallets SET available_minor = available_minor + $3, version = version + 1, updated_at = now()
		WHERE user_id = $1 AND currency = $2
		RETURNING available_minor`, userID, currency, delta).Scan(&balanceAfter)
	return balanceAfter, err
}

// deductBalance 锁钱包并扣减余额，余额不足返回错误。返回变更后余额。
func deductBalance(ctx context.Context, tx pgx.Tx, userID string, currency string, amount int64) (int64, error) {
	var balanceAfter int64
	err := tx.QueryRow(ctx, `
		UPDATE wallets SET available_minor = available_minor - $3, version = version + 1, updated_at = now()
		WHERE user_id = $1 AND currency = $2 AND available_minor >= $3
		RETURNING available_minor`, userID, currency, amount).Scan(&balanceAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		// 区分钱包不存在与余额不足。
		var exists bool
		checkErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wallets WHERE user_id = $1 AND currency = $2)`, userID, currency).Scan(&exists)
		if checkErr != nil {
			return 0, checkErr
		}
		if !exists {
			return 0, ErrInsufficientBalance
		}
		return 0, ErrInsufficientStamina
	}
	return balanceAfter, err
}

// writeLedger 为指定币种的钱包写统一流水，并区分可用与冻结变动。
func writeLedger(ctx context.Context, tx pgx.Tx, userID string, currency string, bizType string, bizID string, amount int64, balanceAfter int64, remark string, operatorID string) error {
	walletID, err := walletIDOf(ctx, tx, userID, currency)
	if err != nil {
		return err
	}
	availableDelta, frozenDelta := amount, int64(0)
	switch bizType {
	case "point_withdrawal":
		frozenDelta = -amount
	case "point_withdrawal_debit":
		availableDelta = 0
		frozenDelta = amount
	case "point_withdrawal_unfreeze":
		frozenDelta = -amount
	}
	_, err = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,remark,operator_id,available_delta_minor,frozen_delta_minor)
        VALUES($1,$2,$3,$4,$3,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10)`, uuid.NewString(), walletID, bizType, bizID, amount, balanceAfter, remark, operatorID, availableDelta, frozenDelta)
	return err
}

func walletIDOf(ctx context.Context, tx pgx.Tx, userID string, currency string) (string, error) {
	var walletID string
	err := tx.QueryRow(ctx, `SELECT id FROM wallets WHERE user_id = $1 AND currency = $2`, userID, currency).Scan(&walletID)
	return walletID, err
}

// findLedgerByBizID 按业务键查询流水（幂等检查）。
func findLedgerByBizID(ctx context.Context, tx pgx.Tx, userID string, currency string, bizType string, bizID string) (CreditResult, error) {
	var result CreditResult
	err := tx.QueryRow(ctx, `
			SELECT wallets.user_id, wallets.currency, ledger_entries.amount_minor, ledger_entries.balance_after_minor, ledger_entries.occurred_at
			FROM ledger_entries JOIN wallets ON wallets.id = ledger_entries.wallet_id
			WHERE wallets.user_id = $1 AND wallets.currency = $2 AND ledger_entries.business_type = $3 AND ledger_entries.business_id = $4`,
		userID, currency, bizType, bizID).
		Scan(&result.UserID, &result.Currency, &result.AmountMinor, &result.BalanceAfterMinor, &result.OccurredAt)
	result.Credited = false
	return result, err
}

func findStaminaLedger(ctx context.Context, tx pgx.Tx, userID string, bizType string, bizID string) (LedgerEntry, error) {
	var entry LedgerEntry
	err := tx.QueryRow(ctx, `
        SELECT le.id,w.user_id,le.business_type,le.business_id,le.amount_minor,le.balance_after_minor,le.remark,COALESCE(le.operator_id::text,''),le.occurred_at
        FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id WHERE w.user_id=$1 AND w.currency='STAMINA' AND le.business_type=$2 AND le.business_id=$3`, userID, bizType, bizID).
		Scan(&entry.ID, &entry.UserID, &entry.BusinessType, &entry.BusinessID, &entry.AmountMinor, &entry.BalanceAfterMinor, &entry.Remark, &entry.OperatorID, &entry.OccurredAt)
	return entry, err
}
