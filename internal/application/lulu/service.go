// Package lulu owns Lulu payment orders. The platform wallet is the only balance authority.
package lulu

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Lulu transfers use whole stones; the existing wallet uses three decimal places.
const Currency = "ORIGIN_STONE"
const Decimals = 3
const stoneScale int64 = 1000

var (
	ErrSameAccount = errors.New("Lulu player account equals platform account")
	ErrInvalid     = errors.New("invalid Lulu request")
	ErrForbidden   = errors.New("Lulu operation is not permitted")
	ErrConflict    = errors.New("Lulu order conflicts with current state or request parameters")
	ErrDisabled    = errors.New("Lulu channel is disabled")
	ErrNotFound    = errors.New("Lulu order not found")
	uidPattern     = regexp.MustCompile(`^[1-9][0-9]{2,19}$`)
)

type PendingDepositError struct {
	LuluUID           string
	RetryAfterSeconds int64
}

func (e *PendingDepositError) Error() string { return "pending Lulu deposit" }
func (e *PendingDepositError) Unwrap() error { return ErrConflict }

func ValidUID(s string) bool { return uidPattern.MatchString(s) }
func ParseAmount(s string) (int64, error) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, ErrInvalid
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/stoneScale {
		return 0, ErrInvalid
	}
	return n, nil
}

type Order struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Kind        string    `json:"kind"`
	RequestID   string    `json:"request_id"`
	LuluUID     string    `json:"lulu_uid"`
	ReceiverUID string    `json:"receiver_uid"`
	Amount      string    `json:"amount"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	ReceiptID   string    `json:"receipt_id,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type Input struct {
	RequestID string `json:"request_id"`
	LuluUID   string `json:"lulu_uid"`
	Amount    string `json:"amount"`
}
type Service struct {
	transferFactory TransferFactory
	balanceFactory  BalanceFactory
	loginFactory    LoginFactory
	encryptionKey   string
	ConfigVersion   int64
	pool            *pgxpool.Pool
	Receiver        string
}

func NewService(pool *pgxpool.Pool, receiver string) *Service {
	return &Service{pool: pool, Receiver: receiver}
}

const columns = `id,user_id,kind,request_id,lulu_uid,receiver_uid,amount::text,status,COALESCE(receipt_id,''),evidence,created_at,expires_at`

func scan(row pgx.Row) (Order, error) {
	var o Order
	o.Currency = Currency
	err := row.Scan(&o.ID, &o.UserID, &o.Kind, &o.RequestID, &o.LuluUID, &o.ReceiverUID, &o.Amount, &o.Status, &o.ReceiptID, &o.Evidence, &o.CreatedAt, &o.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return o, err
}
func conflict(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return ErrConflict
	}
	return err
}

func admin(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrForbidden
	}
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, id).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func player(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrForbidden
	}
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND NOT u.is_virtual AND r.code='player')`, id).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func audit(ctx context.Context, tx pgx.Tx, actor, id, action, evidence string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES(gen_random_uuid(),$1,$2,'lulu_order',$3,jsonb_build_object('evidence',$4::text))`, actor, "lulu."+action, id, evidence)
	return err
}

// balance commits available/frozen deltas with the ledger; the ledger trigger creates the outbox event.
func balance(ctx context.Context, tx pgx.Tx, o Order, typ string, da, df int64, actor string) error {
	if da > math.MaxInt64/stoneScale || da < -math.MaxInt64/stoneScale || df > math.MaxInt64/stoneScale || df < -math.MaxInt64/stoneScale {
		return ErrInvalid
	}
	da *= stoneScale
	df *= stoneScale
	if _, err := tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) VALUES(gen_random_uuid(),$1,'ORIGIN_STONE') ON CONFLICT(user_id,currency) DO NOTHING`, o.UserID); err != nil {
		return err
	}
	var id string
	var a, f int64
	if err := tx.QueryRow(ctx, `SELECT id,available_minor,frozen_minor FROM wallets WHERE user_id=$1 AND currency='ORIGIN_STONE' FOR UPDATE`, o.UserID).Scan(&id, &a, &f); err != nil {
		return err
	}
	if (da < 0 && a < -da) || (df < 0 && f < -df) {
		return wallet.ErrInsufficientFunds
	}
	if (da > 0 && a > math.MaxInt64-da) || (df > 0 && f > math.MaxInt64-df) {
		return ErrInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET available_minor=$2,frozen_minor=$3,version=version+1,updated_at=now() WHERE id=$1`, id, a+da, f+df); err != nil {
		return err
	}
	amount := da
	if da == 0 {
		amount = df
	}
	_, err := tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor,available_delta_minor,frozen_delta_minor,operator_id,remark) VALUES(gen_random_uuid(),$1,$2,$3,$2,$4,$5,$6,$7,NULLIF($8,'')::uuid,'噜噜彩石兑换 ORIGIN_STONE 1:1')`, id, typ, o.ID, amount, a+da, da, df, actor)
	return err
}

func (s *Service) Create(ctx context.Context, user, kind string, in Input) (Order, error) {
	var out Order
	n, err := ParseAmount(in.Amount)
	if err != nil || !ValidUID(in.LuluUID) || len(in.RequestID) == 0 || len(in.RequestID) > 128 || (kind != "deposit" && kind != "withdrawal") {
		return out, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = player(ctx, tx, user); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "lulu_request:"+user+":"+kind+":"+in.RequestID); err != nil {
		return out, err
	}
	out, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM lulu_orders WHERE user_id=$1 AND kind=$2 AND request_id=$3`, user, kind, in.RequestID))
	if err == nil {
		if out.Amount != strconv.FormatInt(n, 10) || out.LuluUID != in.LuluUID {
			return Order{}, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return out, err
	}
	cfg, err := readConfig(ctx, tx, true)
	if err != nil {
		return out, err
	}
	if !cfg.Enabled || !ValidUID(cfg.ReceiverUID) {
		return out, ErrDisabled
	}
	if in.LuluUID == cfg.ReceiverUID {
		return out, ErrSameAccount
	}
	if _, err = wallet.ResolveDecimals(ctx, tx, Currency, true); err != nil {
		return out, err
	}
	if kind == "deposit" {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "lulu_match:"+cfg.ReceiverUID+":"+in.LuluUID); err != nil {
			return out, err
		}
		if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status='expired',updated_at=now() WHERE kind='deposit' AND status='requested' AND expires_at<now() AND receiver_uid=$1 AND lulu_uid=$2`, cfg.ReceiverUID, in.LuluUID); err != nil {
			return out, err
		}
		var remaining int64
		err = tx.QueryRow(ctx, `SELECT GREATEST(1,ceil(extract(epoch FROM expires_at-clock_timestamp())))::bigint FROM lulu_orders WHERE kind='deposit' AND status='requested' AND receiver_uid=$1 AND lulu_uid=$2 LIMIT 1`, cfg.ReceiverUID, in.LuluUID).Scan(&remaining)
		if err == nil {
			return out, &PendingDepositError{LuluUID: in.LuluUID, RetryAfterSeconds: remaining}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	out, err = scan(tx.QueryRow(ctx, `INSERT INTO lulu_orders(user_id,kind,request_id,lulu_uid,receiver_uid,amount,status) VALUES($1,$2,$3,$4,$5,$6,'requested') RETURNING `+columns, user, kind, in.RequestID, in.LuluUID, cfg.ReceiverUID, n))
	if err != nil {
		return out, conflict(err)
	}
	if kind == "deposit" {
		var receiptID string
		err = tx.QueryRow(ctx, `SELECT id FROM lulu_receipts WHERE receiver_uid=$1 AND sender_uid=$2 AND amount=$3 AND order_id IS NULL AND occurred_at >= $4::timestamptz-interval '3 minutes' AND occurred_at <= $5 ORDER BY occurred_at,id LIMIT 1 FOR UPDATE`, out.ReceiverUID, out.LuluUID, n, out.CreatedAt, out.ExpiresAt).Scan(&receiptID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		if err == nil {
			if _, err = tx.Exec(ctx, `UPDATE lulu_receipts SET order_id=$2 WHERE id=$1`, receiptID, out.ID); err != nil {
				return out, err
			}
			if err = balance(ctx, tx, out, "lulu_deposit", n, 0, ""); err != nil {
				return out, err
			}
			if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status='confirmed',receipt_id=$2,updated_at=now() WHERE id=$1`, out.ID, receiptID); err != nil {
				return out, err
			}
			out.Status = "confirmed"
			out.ReceiptID = receiptID
		}
	}
	if kind == "withdrawal" {
		if err = balance(ctx, tx, out, "lulu_withdrawal_freeze", -n, n, ""); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

func (s *Service) List(ctx context.Context, user, actor, kind, status string, limit, offset int) ([]Order, error) {
	switch status {
	case "", "requested", "approved", "sending", "unknown", "confirmed", "rejected", "failed", "expired":
	default:
		return nil, ErrInvalid
	}
	if limit < 1 || limit > 100 || offset < 0 || (kind != "" && kind != "deposit" && kind != "withdrawal") {
		return nil, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if actor != "" {
		err = admin(ctx, tx, actor)
	} else {
		err = player(ctx, tx, user)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status='expired',updated_at=now() WHERE kind='deposit' AND status='requested' AND expires_at<now() AND ($1='' OR user_id::text=$1)`, user); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM lulu_orders WHERE ($1='' OR user_id::text=$1) AND ($2='' OR kind=$2) AND ($3='' OR status=$3) ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, user, kind, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, tx.Commit(ctx)
}

// Review records staff decisions and external reconciliation evidence.
// Ordinary deposits are credited by RecordReceipt; withdrawal approval only queues a transfer.
func (s *Service) Review(ctx context.Context, id, actor, action, evidence string) (Order, error) {
	var o Order
	if _, err := uuid.Parse(id); err != nil {
		return o, ErrInvalid
	}
	if len(strings.TrimSpace(evidence)) == 0 || len(evidence) > 2000 {
		return o, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return o, err
	}
	defer tx.Rollback(ctx)
	if err = admin(ctx, tx, actor); err != nil {
		return o, err
	}
	cfg, configErr := readConfig(ctx, tx, true)
	if configErr != nil {
		return o, configErr
	}
	o, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM lulu_orders WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return o, err
	}
	if o.Kind != "withdrawal" {
		return o, ErrConflict
	}
	n, _ := ParseAmount(o.Amount)
	target := ""
	switch action {
	case "approve":
		target = "approved"
		if o.Status == target {
			return o, tx.Commit(ctx)
		}
		if o.Status != "requested" {
			return o, ErrConflict
		}
		if !cfg.Enabled || o.ReceiverUID != cfg.ReceiverUID {
			return o, ErrDisabled
		}
	case "reject":
		target = "rejected"
		if o.Status == target {
			return o, tx.Commit(ctx)
		}
		if o.Status != "requested" {
			return o, ErrConflict
		}
		err = balance(ctx, tx, o, "lulu_withdrawal_unfreeze", n, -n, actor)
	case "confirm_paid", "confirm_not_paid":
		// Only unknown transfers can be resolved manually, with external evidence and audit.
		target = "confirmed"
		if action == "confirm_not_paid" {
			target = "failed"
		}
		if o.Status == target {
			return o, tx.Commit(ctx)
		}
		if o.Status != "unknown" {
			return o, ErrConflict
		}
		if action == "confirm_paid" {
			err = balance(ctx, tx, o, "lulu_withdrawal_debit", 0, -n, actor)
		} else {
			err = balance(ctx, tx, o, "lulu_withdrawal_unfreeze", n, -n, actor)
		}
	default:
		return o, ErrInvalid
	}
	if err != nil {
		return o, err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status=$2,evidence=$3,reviewed_by=$4,updated_at=now() WHERE id=$1`, id, target, evidence, actor); err != nil {
		return o, err
	}
	if err = audit(ctx, tx, actor, id, action, evidence); err != nil {
		return o, err
	}
	o.Status = target
	o.Evidence = evidence
	return o, tx.Commit(ctx)
}
