package credit

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidAdjustment = errors.New("资金操作参数无效")
var ErrAdjustmentConflict = errors.New("请求编号已用于其他资金操作，请勿修改参数后重用")
var ErrAdjustmentForbidden = errors.New("仅管理员或运营人员可以操作资金")

type AdjustmentInput struct {
	UserID     string `json:"user_id"`
	Currency   string `json:"currency"`
	Action     string `json:"action"`
	Amount     string `json:"amount"`
	Remark     string `json:"remark"`
	RequestID  string `json:"request_id"`
	OperatorID string `json:"-"`
}

type AdjustmentResult struct {
	OperationID        string    `json:"operation_id"`
	UserID             string    `json:"user_id"`
	Currency           string    `json:"currency"`
	Action             string    `json:"action"`
	AmountMinor        int64     `json:"amount_minor"`
	DeltaMinor         int64     `json:"delta_minor"`
	BalanceBeforeMinor int64     `json:"balance_before_minor"`
	BalanceAfterMinor  int64     `json:"balance_after_minor"`
	FrozenMinor        int64     `json:"frozen_minor"`
	Duplicate          bool      `json:"duplicate"`
	OccurredAt         time.Time `json:"occurred_at"`
}

func adjustmentDirection(action string) (int64, bool) {
	switch action {
	case "credit", "reward":
		return 1, true
	case "debit", "penalty":
		return -1, true
	default:
		return 0, false
	}
}

// AdjustWallet 仅处理平台余额，不创建提现单、不调用链上付款。
// 密码由 HTTP 入口验证；这里再次检查数据库中的当前管理员权限。
func (s *Service) AdjustWallet(ctx context.Context, in AdjustmentInput) (AdjustmentResult, error) {
	var out AdjustmentResult
	direction, ok := adjustmentDirection(in.Action)
	if !ok || in.UserID == "" || in.RequestID == "" || len(in.RequestID) > 128 || len(in.Remark) > 2000 {
		return out, ErrInvalidAdjustment
	}
	if in.OperatorID == "" {
		return out, ErrAdjustmentForbidden
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if !validCurrency(in.Currency) {
		return out, ErrInvalidCurrency
	}
	if !wallet.ValidDisplayAmount(in.Amount) {
		return out, ErrInvalidAmount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var admin bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, in.OperatorID).Scan(&admin)
	if err != nil {
		return out, err
	}
	if !admin {
		return out, ErrAdjustmentForbidden
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "wallet_adjustment:"+in.OperatorID+":"+in.RequestID); err != nil {
		return out, err
	}
	// 精度不可变。先检查重复结果，再检查币种当前是否启用，确保停用后仍可安全重试。
	decimals, err := wallet.ResolveDecimals(ctx, tx, in.Currency, false)
	if errors.Is(err, wallet.ErrUnknownCurrency) {
		return out, ErrInvalidCurrency
	}
	if err != nil {
		return out, err
	}
	amount, err := wallet.ParseDisplayAmount(in.Amount, decimals)
	if err != nil {
		return out, ErrInvalidAmount
	}
	var oldUser, oldCurrency, oldAction, oldRemark string
	var oldAmount int64
	var data []byte
	err = tx.QueryRow(ctx, `SELECT user_id::text,currency,action,amount_minor,remark,result FROM admin_wallet_adjustments WHERE operator_id=$1 AND request_id=$2`, in.OperatorID, in.RequestID).Scan(&oldUser, &oldCurrency, &oldAction, &oldAmount, &oldRemark, &data)
	if err == nil {
		if oldUser != in.UserID || oldCurrency != in.Currency || oldAction != in.Action || oldAmount != amount || oldRemark != in.Remark {
			return out, ErrAdjustmentConflict
		}
		if err = json.Unmarshal(data, &out); err != nil {
			return out, err
		}
		out.Duplicate = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// 升级前上分接口以 request_id 作为流水业务编号。保留重放保护，
	// 不将旧请求误认为新操作；旧编号跨钱包复用的歧义情况拒绝自动处理。
	var legacyCount int
	err = tx.QueryRow(ctx, `SELECT le.id::text,w.user_id::text,w.currency,le.amount_minor,
		le.balance_after_minor,COALESCE(le.frozen_after_minor,0),le.remark,le.occurred_at,count(*) OVER()
		FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id
		WHERE le.operator_id=$1 AND le.business_type='admin_credit' AND le.business_id=$2
		AND NOT EXISTS(SELECT 1 FROM admin_wallet_adjustments a WHERE a.id::text=le.business_id)
		LIMIT 1`, in.OperatorID, in.RequestID).Scan(&out.OperationID, &out.UserID, &out.Currency, &out.AmountMinor, &out.BalanceAfterMinor, &out.FrozenMinor, &oldRemark, &out.OccurredAt, &legacyCount)
	if err == nil {
		if legacyCount != 1 || in.Action != "credit" || out.UserID != in.UserID || out.Currency != in.Currency || out.AmountMinor != amount || oldRemark != in.Remark {
			return AdjustmentResult{}, ErrAdjustmentConflict
		}
		out.Action = "credit"
		out.DeltaMinor = amount
		out.BalanceBeforeMinor = out.BalanceAfterMinor - amount
		out.Duplicate = true
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if _, err = wallet.ResolveDecimals(ctx, tx, in.Currency, true); err != nil {
		if errors.Is(err, wallet.ErrCurrencyDisabled) {
			return out, ErrInvalidCurrency
		}
		return out, err
	}
	var virtual bool
	err = tx.QueryRow(ctx, `SELECT is_virtual FROM users WHERE id=$1 FOR SHARE`, in.UserID).Scan(&virtual)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrUserNotFound
	}
	if err != nil {
		return out, err
	}
	if virtual && direction < 0 {
		return out, ErrVirtualAccountWithdrawal
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES($1,$2,$3) ON CONFLICT(user_id,currency) DO NOTHING`, uuid.NewString(), in.UserID, in.Currency); err != nil {
		return out, err
	}
	var before, frozen int64
	err = tx.QueryRow(ctx, `SELECT available_minor,frozen_minor FROM wallets WHERE user_id=$1 AND currency=$2 FOR UPDATE`, in.UserID, in.Currency).Scan(&before, &frozen)
	if err != nil {
		return out, err
	}
	if direction < 0 && before < amount {
		return out, ErrInsufficientBalance
	}
	if direction > 0 && (before > math.MaxInt64-frozen || amount > math.MaxInt64-frozen-before) {
		return out, ErrInvalidAmount
	}
	after := before + direction*amount
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_minor=$3,version=version+1,updated_at=now() WHERE user_id=$1 AND currency=$2`, in.UserID, in.Currency, after); err != nil {
		return out, err
	}
	out = AdjustmentResult{OperationID: uuid.NewString(), UserID: in.UserID, Currency: in.Currency, Action: in.Action, AmountMinor: amount, DeltaMinor: direction * amount, BalanceBeforeMinor: before, BalanceAfterMinor: after, FrozenMinor: frozen, OccurredAt: time.Now().UTC()}
	if err = writeLedger(ctx, tx, in.UserID, in.Currency, "admin_"+in.Action, out.OperationID, out.DeltaMinor, after, in.Remark, in.OperatorID); err != nil {
		return out, err
	}
	data, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO admin_wallet_adjustments(id,operator_id,request_id,user_id,currency,action,amount_minor,remark,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, out.OperationID, in.OperatorID, in.RequestID, in.UserID, in.Currency, in.Action, amount, in.Remark, data); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,request_id,payload) VALUES($1,$2,$3,'user',$4,$5,$6)`, uuid.NewString(), in.OperatorID, "admin.wallet."+in.Action, in.UserID, in.RequestID, data); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
