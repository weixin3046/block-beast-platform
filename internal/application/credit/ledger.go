package credit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/wallet"
	"github.com/google/uuid"
)

var ErrInvalidCursor = errors.New("invalid ledger cursor")

type LedgerPage struct {
	Items      []UnifiedLedgerEntry `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}
type UnifiedLedgerEntry struct {
	ID                  string    `json:"id"`
	Currency            string    `json:"currency"`
	Decimals            int       `json:"decimals"`
	BusinessType        string    `json:"business_type"`
	BusinessID          string    `json:"business_id"`
	EntryType           string    `json:"entry_type"`
	AmountMinor         int64     `json:"amount_minor"`
	Amount              string    `json:"amount"`
	AvailableDeltaMinor int64     `json:"available_delta_minor"`
	FrozenDeltaMinor    int64     `json:"frozen_delta_minor"`
	AvailableAfterMinor int64     `json:"available_after_minor"`
	FrozenAfterMinor    *int64    `json:"frozen_after_minor"`
	AvailableAfter      string    `json:"available_after"`
	FrozenAfter         *string   `json:"frozen_after"`
	Remark              string    `json:"remark"`
	OccurredAt          time.Time `json:"occurred_at"`
}
type ledgerCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func decodeLedgerCursor(value string) (ledgerCursor, error) {
	var out ledgerCursor
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(b) > 512 || json.Unmarshal(b, &out) != nil || out.At.IsZero() {
		return out, ErrInvalidCursor
	}
	if _, err := uuid.Parse(out.ID); err != nil {
		return out, ErrInvalidCursor
	}
	return out, nil
}
func (service *Service) ListUnifiedLedger(ctx context.Context, userID, currency, cursor string, limit int) (LedgerPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	var at *time.Time
	var id *string
	if cursor != "" {
		c, err := decodeLedgerCursor(cursor)
		if err != nil {
			return LedgerPage{}, err
		}
		at = &c.At
		id = &c.ID
	}
	rows, err := service.pool.Query(ctx, `SELECT l.id,w.currency,c.decimals,l.business_type,l.business_id,l.entry_type,l.amount_minor,l.available_delta_minor,l.frozen_delta_minor,l.balance_after_minor,l.frozen_after_minor,l.remark,l.occurred_at
      FROM ledger_entries l JOIN wallets w ON w.id=l.wallet_id JOIN currencies c ON c.code=w.currency
      WHERE w.user_id=$1 AND ($2='' OR w.currency=$2)
        AND ($3::timestamptz IS NULL OR (l.occurred_at,l.id)<($3::timestamptz,$4::uuid))
      ORDER BY l.occurred_at DESC,l.id DESC LIMIT $5`, userID, currency, at, id, limit+1)
	if err != nil {
		return LedgerPage{}, err
	}
	defer rows.Close()
	out := LedgerPage{Items: []UnifiedLedgerEntry{}}
	for rows.Next() {
		var v UnifiedLedgerEntry
		if err := rows.Scan(&v.ID, &v.Currency, &v.Decimals, &v.BusinessType, &v.BusinessID, &v.EntryType, &v.AmountMinor, &v.AvailableDeltaMinor, &v.FrozenDeltaMinor, &v.AvailableAfterMinor, &v.FrozenAfterMinor, &v.Remark, &v.OccurredAt); err != nil {
			return LedgerPage{}, err
		}
		v.Amount, _ = wallet.FormatDisplayAmount(v.AmountMinor, v.Decimals)
		v.AvailableAfter, _ = wallet.FormatDisplayAmount(v.AvailableAfterMinor, v.Decimals)
		if v.FrozenAfterMinor != nil {
			value, _ := wallet.FormatDisplayAmount(*v.FrozenAfterMinor, v.Decimals)
			v.FrozenAfter = &value
		}
		out.Items = append(out.Items, v)
	}
	if err := rows.Err(); err != nil {
		return LedgerPage{}, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		encoded, _ := json.Marshal(ledgerCursor{At: last.OccurredAt, ID: last.ID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return out, nil
}
