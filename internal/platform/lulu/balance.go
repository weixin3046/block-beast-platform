package lulu

import (
	"context"
	"encoding/json"
	"errors"
	app "github.com/block-beast/platform/internal/application/lulu"
	"regexp"
)

var balanceDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func itemBalance(item map[string]any) (string, error) {
	value, ok := item["item_num"].(json.Number)
	if !ok || !balanceDecimal.MatchString(value.String()) {
		return "", app.ErrBalanceUnavailable
	}
	return value.String(), nil
}
func (c *Client) Balance(ctx context.Context) (string, error) {
	v, err := c.call(ctx, "GET", "/player/item?item_id=102201", nil)
	if err != nil {
		return "", balanceError(err)
	}
	if !success(v) {
		return "", app.ErrBalanceUnavailable
	}
	if item, ok := v["data"].(map[string]any); ok && str(item["item_id"]) == "102201" {
		return itemBalance(item)
	}
	v, err = c.call(ctx, "GET", "/player/items", nil)
	if err != nil {
		return "", balanceError(err)
	}
	if !success(v) {
		return "", app.ErrBalanceUnavailable
	}
	items, ok := v["data"].([]any)
	if !ok {
		return "", app.ErrBalanceUnavailable
	}
	var found map[string]any
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && str(item["item_id"]) == "102201" {
			if found != nil {
				return "", app.ErrBalanceUnavailable
			}
			found = item
		}
	}
	if found == nil {
		return "", app.ErrBalanceUnavailable
	}
	return itemBalance(found)
}
func balanceError(err error) error {
	if errors.Is(err, app.ErrTokenInvalid) {
		return err
	}
	return app.ErrBalanceUnavailable
}
