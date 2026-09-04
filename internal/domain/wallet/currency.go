package wallet

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrUnknownCurrency = errors.New("currency is not registered")
var ErrCurrencyDisabled = errors.New("currency is disabled for new operations")

type CurrencyReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ResolveDecimals reads the canonical platform unit, not the network unit.
// Settlement/refund callers use enabledOnly=false to finish existing obligations.
func ResolveDecimals(ctx context.Context, db CurrencyReader, code string, enabledOnly bool) (int, error) {
	var decimals int
	var enabled bool
	err := db.QueryRow(ctx, `SELECT decimals,enabled FROM currencies WHERE code=$1`, strings.ToUpper(strings.TrimSpace(code))).Scan(&decimals, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUnknownCurrency
	}
	if err != nil {
		return 0, err
	}
	if enabledOnly && !enabled {
		return 0, ErrCurrencyDisabled
	}
	return decimals, nil
}
