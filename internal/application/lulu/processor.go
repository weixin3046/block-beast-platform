package lulu

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrTokenInvalid = errors.New("Lulu token invalid")

type Receipt struct {
	ID          string    `json:"id"`
	ReceiverUID string    `json:"receiver_uid"`
	SenderUID   string    `json:"sender_uid"`
	Amount      int64     `json:"-"`
	OccurredAt  time.Time `json:"occurred_at"`
}
type TransferResult string

const (
	TransferConfirmed TransferResult = "confirmed"
	TransferUnknown   TransferResult = "unknown"
	// Only a provider response known to guarantee no transfer may return this value.
	TransferFailed TransferResult = "failed"
)

type Provider interface {
	Receipts(context.Context, time.Time, func(Receipt) error) error
	Transfer(context.Context, Order) TransferResult
}

// RecordReceipt is only called by the trusted collector, never by a player HTTP endpoint.
func (s *Service) RecordReceipt(ctx context.Context, r Receipt) error {
	if r.ID == "" || len(r.ID) > 256 || !ValidUID(r.SenderUID) || r.ReceiverUID != s.Receiver || r.Amount <= 0 || r.OccurredAt.IsZero() {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO lulu_receipts(id,receiver_uid,sender_uid,amount,occurred_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, r.ID, r.ReceiverUID, r.SenderUID, r.Amount, r.OccurredAt); err != nil {
		return err
	}
	var stored Receipt
	var order *string
	err = tx.QueryRow(ctx, `SELECT receiver_uid,sender_uid,amount,occurred_at,order_id::text FROM lulu_receipts WHERE id=$1 FOR UPDATE`, r.ID).Scan(&stored.ReceiverUID, &stored.SenderUID, &stored.Amount, &stored.OccurredAt, &order)
	if err != nil {
		return err
	}
	if stored.ReceiverUID != r.ReceiverUID || stored.SenderUID != r.SenderUID || stored.Amount != r.Amount || !stored.OccurredAt.Equal(r.OccurredAt) {
		return ErrConflict
	}
	if order != nil {
		return tx.Commit(ctx)
	}
	// The user-approved policy credits actual receipts matching a pre-existing order.
	// UID is a routing claim, not proof of account ownership. Late receipts can complete expired orders.
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM lulu_orders WHERE kind='deposit' AND status IN ('requested','expired') AND receiver_uid=$1 AND lulu_uid=$2 AND amount=$3 AND created_at<=$4 AND expires_at>=$4 ORDER BY created_at LIMIT 1 FOR UPDATE`, r.ReceiverUID, r.SenderUID, r.Amount, r.OccurredAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_receipts SET order_id=$2 WHERE id=$1`, r.ID, id); err != nil {
		return err
	}
	o, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM lulu_orders WHERE id=$1`, id))
	if err != nil {
		return err
	}
	if err = balance(ctx, tx, o, "lulu_deposit", r.Amount, 0, ""); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status='confirmed',receipt_id=$2,updated_at=now() WHERE id=$1`, id, r.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Dispatch commits sending before the remote call. A crash leaves an ambiguous
// order that is never automatically resent, even after process restart.
func (s *Service) Dispatch(ctx context.Context, p Provider) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cfg, err := readConfig(ctx, tx, true)
	if err != nil {
		return err
	}
	if !cfg.Enabled || cfg.ReceiverUID != s.Receiver || (s.ConfigVersion != 0 && cfg.Version != s.ConfigVersion) {
		return nil
	}
	o, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM lulu_orders WHERE kind='withdrawal' AND status='approved' AND receiver_uid=$1 ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, s.Receiver))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status='sending',updated_at=now() WHERE id=$1`, o.ID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	result := p.Transfer(ctx, o)
	// Use a fresh bounded context for persistence if the request context was cancelled.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return s.finish(saveCtx, o.ID, result)
}
func (s *Service) finish(ctx context.Context, id string, result TransferResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	o, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM lulu_orders WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if o.Status != "sending" {
		return ErrConflict
	}
	n, _ := ParseAmount(o.Amount)
	switch result {
	case TransferConfirmed:
		err = balance(ctx, tx, o, "lulu_withdrawal_debit", 0, -n, "")
	case TransferFailed:
		err = balance(ctx, tx, o, "lulu_withdrawal_unfreeze", n, -n, "")
	default:
		result = TransferUnknown
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE lulu_orders SET status=$2,updated_at=now() WHERE id=$1`, id, string(result)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RecoverSending is run under the exclusive process advisory lock on startup.
func (s *Service) RecoverSending(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE lulu_orders SET status='unknown',updated_at=now() WHERE status='sending' AND kind='withdrawal' AND receiver_uid=$1`, s.Receiver)
	return err
}

func (s *Service) Collect(ctx context.Context, p Provider, start time.Time) error {
	cfg, configErr := s.Config(ctx)
	if configErr != nil {
		return configErr
	}
	if !cfg.Enabled || cfg.ReceiverUID != s.Receiver || (s.ConfigVersion != 0 && cfg.Version != s.ConfigVersion) {
		return nil
	}
	if start.IsZero() {
		return ErrInvalid
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO lulu_collector_state(receiver_uid) VALUES($1) ON CONFLICT DO NOTHING`, s.Receiver); err != nil {
		return err
	}
	var watermark *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT scanned_at FROM lulu_collector_state WHERE receiver_uid=$1`, s.Receiver).Scan(&watermark); err != nil {
		return err
	}
	since := start
	if watermark != nil && watermark.Add(-10*time.Minute).After(since) {
		since = watermark.Add(-10 * time.Minute)
	}
	scanStarted := time.Now().UTC()
	err := p.Receipts(ctx, since, func(r Receipt) error { return s.RecordReceipt(ctx, r) })
	if err != nil {
		// Never store raw provider responses: they may contain credentials or personal information.
		message := "collection failed; cursor unchanged"
		if errors.Is(err, ErrTokenInvalid) {
			message = "噜噜登录已失效，请重新获取短信验证码登录"
		}
		_, saveErr := s.pool.Exec(ctx, `UPDATE lulu_collector_state SET last_error=$2,last_cycle=$2,down_at=CASE WHEN $3 AND down_at=0 THEN floor(extract(epoch FROM clock_timestamp())*1000)::bigint ELSE down_at END WHERE receiver_uid=$1`, s.Receiver, message, errors.Is(err, ErrTokenInvalid))
		if saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE lulu_collector_state SET scanned_at=$2,last_success_at=now(),last_error='',last_cycle=CASE WHEN down_at>0 THEN '噜噜登录已恢复，采集成功' ELSE '采集成功' END,recovered_at=CASE WHEN down_at>0 THEN floor(extract(epoch FROM clock_timestamp())*1000)::bigint ELSE recovered_at END,last_down_ms=CASE WHEN down_at>0 THEN GREATEST(0,floor(extract(epoch FROM clock_timestamp())*1000)::bigint-down_at) ELSE last_down_ms END,down_at=0 WHERE receiver_uid=$1`, s.Receiver, scanStarted)
	return err
}

type Health struct {
	LastCycle    string     `json:"last_cycle"`
	DownAt       int64      `json:"down_at"`
	RecoveredAt  int64      `json:"recovered_at"`
	LastDownMS   int64      `json:"last_down_ms"`
	TokenInvalid bool       `json:"token_invalid"`
	ReceiverUID  string     `json:"receiver_uid"`
	LastSuccess  *time.Time `json:"last_success_at"`
	LastError    string     `json:"last_error"`
}

func (s *Service) Health(ctx context.Context, actor string) (Health, error) {
	out := Health{}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = admin(ctx, tx, actor); err != nil {
		return out, err
	}
	cfg, err := readConfig(ctx, tx, false)
	if err != nil {
		return out, err
	}
	out.ReceiverUID = cfg.ReceiverUID
	err = tx.QueryRow(ctx, `SELECT last_success_at,last_error,last_cycle,down_at,recovered_at,last_down_ms FROM lulu_collector_state WHERE receiver_uid=$1`, out.ReceiverUID).Scan(&out.LastSuccess, &out.LastError, &out.LastCycle, &out.DownAt, &out.RecoveredAt, &out.LastDownMS)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	out.TokenInvalid = out.DownAt > 0 || out.LastError == "噜噜登录已失效，请重新获取短信验证码登录"
	return out, err
}
