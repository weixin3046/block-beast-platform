package lulu

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type TransferRecord struct {
	ID              string    `json:"id"`
	Direction       string    `json:"direction"`
	CounterpartyUID string    `json:"counterparty_uid"`
	Nickname        string    `json:"nickname"`
	ItemID          int64     `json:"item_id"`
	Amount          string    `json:"amount"`
	OccurredAt      time.Time `json:"occurred_at"`
	Linked          bool      `json:"linked"`
	OrderID         string    `json:"order_id,omitempty"`
	OrderStatus     string    `json:"order_status,omitempty"`
	LinkMethod      string    `json:"link_method,omitempty"`
	LinkReason      string    `json:"link_reason,omitempty"`
}
type TransferPage struct {
	ReceiverUID string           `json:"receiver_uid"`
	Page        int              `json:"page"`
	Size        int              `json:"size"`
	Total       int64            `json:"total"`
	Items       []TransferRecord `json:"items"`
}
type TransferReader interface {
	TransferRecords(context.Context, string, int, int) (TransferPage, error)
}
type TransferFactory func(string, string, string, string) (TransferReader, error)

func (s *Service) WithTransferFactory(f TransferFactory) *Service { s.transferFactory = f; return s }
func (s *Service) Transfers(ctx context.Context, actor, direction string, page, size int) (TransferPage, error) {
	var out TransferPage
	if (direction != "received" && direction != "sent") || page < 1 || page > 1000 || size < 1 || size > 100 {
		return out, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	if e = admin(ctx, tx, actor); e != nil {
		return out, e
	}
	if e = tx.Commit(ctx); e != nil {
		return out, e
	}
	cfg, e := s.RuntimeConfig(ctx)
	if e != nil {
		return out, e
	}
	if !cfg.Enabled {
		return out, ErrDisabled
	}
	if s.transferFactory == nil {
		return out, ErrDisabled
	}
	reader, e := s.transferFactory(cfg.APIURL, cfg.ReceiverUID, cfg.Token, cfg.ProtocolKey)
	if e != nil {
		return out, e
	}
	out, e = reader.TransferRecords(ctx, direction, page, size)
	if e != nil {
		return out, e
	}
	for i := range out.Items {
		r := &out.Items[i]
		r.LinkReason = "no_matching_receipt"
		if direction == "sent" {
			if e = s.linkWithdrawal(ctx, cfg.ReceiverUID, r); e != nil {
				return TransferPage{}, e
			}
			continue
		}
		e = s.pool.QueryRow(ctx, `SELECT o.id::text,o.status FROM lulu_receipts r JOIN lulu_orders o ON o.id=r.order_id WHERE r.id=$1 AND r.receiver_uid=$2 AND o.kind='deposit'`, r.ID, cfg.ReceiverUID).Scan(&r.OrderID, &r.OrderStatus)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return TransferPage{}, e
		}
		if e == nil {
			r.Linked = true
			r.LinkMethod = "receipt_id"
			r.LinkReason = ""
		}
	}
	// Two upstream rows must not display the same inferred order association.
	counts := map[string]int{}
	for _, r := range out.Items {
		if r.Linked {
			counts[r.OrderID]++
		}
	}
	if direction == "sent" {
		for i := range out.Items {
			r := &out.Items[i]
			if r.Linked && counts[r.OrderID] > 1 {
				r.Linked, r.OrderID, r.OrderStatus, r.LinkMethod = false, "", "", ""
				r.LinkReason = "ambiguous_transfer"
			}
		}
	}
	return out, nil
}

// Outgoing records have no upstream transaction ID. This is an explicitly
// labelled inference for display only, never evidence for settling funds.
type withdrawalLink struct{ ID, Status string }

func applyWithdrawalLink(r *TransferRecord, candidates []withdrawalLink) {
	r.Linked, r.OrderID, r.OrderStatus, r.LinkMethod = false, "", "", ""
	if r.ItemID != 102201 {
		r.LinkReason = "unsupported_item"
		return
	}
	if !strings.HasPrefix(r.Amount, "-") {
		r.LinkReason = "invalid_transfer_amount"
		return
	}
	if _, err := ParseAmount(strings.TrimPrefix(r.Amount, "-")); err != nil {
		r.LinkReason = "invalid_transfer_amount"
		return
	}
	r.LinkReason = "no_matching_withdrawal"
	if len(candidates) > 1 {
		r.LinkReason = "ambiguous_withdrawal"
		return
	}
	if len(candidates) == 1 {
		r.Linked, r.OrderID, r.OrderStatus = true, candidates[0].ID, candidates[0].Status
		r.LinkMethod, r.LinkReason = "account_uid_amount_completion_window", ""
	}
}

func (s *Service) linkWithdrawal(ctx context.Context, receiver string, r *TransferRecord) error {
	applyWithdrawalLink(r, nil)
	if r.LinkReason != "no_matching_withdrawal" {
		return nil
	}
	start := r.OccurredAt
	// Allow thirty seconds of clock/recording skew in either direction.
	// Use the immutable debit ledger time, excluding manual reconciliation.
	// LIMIT 2 is sufficient to reject non-unique candidates.
	rows, err := s.pool.Query(ctx, `SELECT o.id::text,o.status
        FROM lulu_orders o
        WHERE o.kind='withdrawal' AND o.status='confirmed'
          AND o.receiver_uid=$1 AND o.lulu_uid=$2 AND o.amount=$3::bigint
          AND EXISTS (SELECT 1 FROM ledger_entries l
            WHERE l.business_id=o.id::text AND l.business_type='lulu_withdrawal_debit'
              AND l.entry_type='lulu_withdrawal_debit' AND l.operator_id IS NULL
              AND l.occurred_at >= $4 AND l.occurred_at <= $5)
        LIMIT 2`, receiver, r.CounterpartyUID, strings.TrimPrefix(r.Amount, "-"), start.Add(-30*time.Second), start.Add(30*time.Second))
	if err != nil {
		return err
	}
	defer rows.Close()
	var candidates []withdrawalLink
	for rows.Next() {
		var c withdrawalLink
		if err := rows.Scan(&c.ID, &c.Status); err != nil {
			return err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	applyWithdrawalLink(r, candidates)
	return nil
}
