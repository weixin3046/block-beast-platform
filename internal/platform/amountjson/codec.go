package amountjson

// The public JSON boundary uses base-unit decimal amounts. Application services,
// ledger transactions and event storage continue to use integer minor units.
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/block-beast/platform/internal/application/currency"
	"io"
	"strings"

	"github.com/block-beast/platform/internal/domain/wallet"
)

type Catalog interface {
	List(context.Context, bool) ([]currency.Currency, error)
}

func New(ctx context.Context, catalog Catalog) *Codec { return &Codec{Catalog: catalog, ctx: ctx} }

type Codec struct {
	Catalog  Catalog
	ctx      context.Context
	decimals map[string]int
	enabled  map[string]bool
}

func (c *Codec) precision(code string, input bool) (int, error) {
	if c.decimals == nil {
		if c.Catalog == nil {
			return 0, fmt.Errorf("币种服务不可用")
		}
		rows, err := c.Catalog.List(c.ctx, false)
		if err != nil {
			return 0, err
		}
		c.decimals = map[string]int{}
		c.enabled = map[string]bool{}
		for _, v := range rows {
			c.decimals[v.Code] = v.Decimals
			c.enabled[v.Code] = v.Enabled
		}
	}
	d, ok := c.decimals[strings.ToUpper(code)]
	if !ok || (input && !c.enabled[strings.ToUpper(code)]) {
		return 0, fmt.Errorf("币种不存在或未启用：%s", code)
	}
	return d, nil
}

// These are monetary fields only: odds, ranks, weights and counts are untouched.
var publicMoneyFields = strings.Fields(`amount stake payout threshold progress reward cost total remaining available frozen balance_after balance_before balance_after_bet balance_after_settlement balance_after_refund delta available_delta frozen_delta available_after frozen_after effective_stake total_payout net_win valid_stake paid_commission guess_max_stake dodge_max_stake road_max_stake min_stake max_stake clearance gift penalty deposit credit balance cost_balance_after_spin refund`)

func moneyCurrency(m map[string]any, field, inherited string) string {
	code := inherited
	if v, ok := m["currency"].(string); ok && v != "" {
		code = v
	}
	if code == "" {
		if v, ok := m["token_code"].(string); ok {
			code = v
		}
	}
	if strings.HasPrefix(field, "cost") {
		if v, ok := m["cost_currency"].(string); ok {
			return v
		}
	}
	if field == "threshold" || field == "progress" {
		if v, ok := m["accumulation_currency"].(string); ok {
			return v
		}
	}
	if field == "reward" || field == "reward_balance" || field == "balance_after" {
		if v, ok := m["reward_currency"].(string); ok {
			return v
		}
	}
	return code
}

func decimalText(v any) (string, error) {
	switch n := v.(type) {
	case json.Number:
		return n.String(), nil
	case string:
		return n, nil
	}
	return "", fmt.Errorf("金额必须是数字或十进制字符串")
}

// Recursion carries the currency for leaderboard entries and currency-keyed limits.
func (c *Codec) Convert(v any, inherited string, input, displayAdjustment bool) error {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if err := c.Convert(item, inherited, input, displayAdjustment); err != nil {
				return err
			}
		}
	case map[string]any:
		if input {
			for _, key := range []string{"currency", "cost_currency", "reward_currency", "accumulation_currency"} {
				if value, ok := x[key].(string); ok {
					x[key] = strings.ToUpper(strings.TrimSpace(value))
				}
			}
		}
		code := moneyCurrency(x, "", inherited)
		for key, child := range x {
			if key == "selection" {
				continue
			}
			next := code
			if key == "bet_limits" || key == "initial_balances" {
				limits, ok := child.(map[string]any)
				if !ok {
					return fmt.Errorf("%s 格式错误", key)
				}
				for currency, value := range limits {
					if key == "initial_balances" {
						d, err := c.precision(currency, input)
						if err != nil {
							return err
						}
						text, err := decimalText(value)
						if err != nil {
							return err
						}
						if input {
							n, err := wallet.ParseDisplayAmount(text, d)
							if err != nil {
								return fmt.Errorf("%s 金额精度无效", currency)
							}
							limits[currency] = json.Number(fmt.Sprint(n))
						} else {
							n, err := json.Number(text).Int64()
							if err != nil {
								return err
							}
							s, err := wallet.FormatDisplayAmount(n, d)
							if err != nil {
								return err
							}
							limits[currency] = s
						}
					} else if err := c.Convert(value, currency, input, displayAdjustment); err != nil {
						return err
					}
				}
				continue
			}
			if err := c.Convert(child, next, input, displayAdjustment); err != nil {
				return err
			}
		}
		for _, field := range publicMoneyFields {
			minor := field + "_minor"
			value, exists := x[minor]
			if input {
				if exists {
					return fmt.Errorf("请使用实际金额字段 %s，不再接收 %s", field, minor)
				}
				value, exists = x[field]
				if !exists {
					continue
				}
				if _, object := value.(map[string]any); object {
					continue
				}
			} else if !exists {
				continue
			}
			if value == nil {
				if !input {
					x[field] = nil
					delete(x, minor)
				}
				continue
			}
			text, err := decimalText(value)
			if err != nil {
				return fmt.Errorf("%s：%w", field, err)
			}
			d, err := c.precision(moneyCurrency(x, field, code), input)
			if err != nil {
				return err
			}
			if input {
				n, err := wallet.ParseDisplayAmount(text, d)
				if err != nil {
					return fmt.Errorf("%s 必须大于0，且最多支持%d位小数", field, d)
				}
				if displayAdjustment && field == "amount" {
					x[field] = text
					continue
				}
				delete(x, field)
				x[minor] = json.Number(fmt.Sprint(n))
			} else {
				n, err := json.Number(text).Int64()
				if err != nil {
					return err
				}
				s, err := wallet.FormatDisplayAmount(n, d)
				if err != nil {
					return err
				}
				x[field] = s
				delete(x, minor)
			}
		}
		if !input {
			for _, field := range []string{"cost_balance", "reward_balance"} {
				if n, ok := x[field].(json.Number); ok {
					d, err := c.precision(moneyCurrency(x, field, code), false)
					if err != nil {
						return err
					}
					i, err := n.Int64()
					if err != nil {
						return err
					}
					s, err := wallet.FormatDisplayAmount(i, d)
					if err != nil {
						return err
					}
					x[field] = s
				}
			}
		}
	}
	return nil
}

func ReadJSON(r io.Reader) (any, error) {
	d := json.NewDecoder(r)
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("只能提交一个JSON对象")
	}
	return v, nil
}

func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON嵌套过深")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		out := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("JSON字段无效")
			}
			if _, exists := out[name]; exists {
				return nil, fmt.Errorf("JSON字段不能重复")
			}
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out[name] = value
		}
		_, err = d.Token()
		return out, err
	case json.Delim('['):
		out := []any{}
		for d.More() {
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		_, err = d.Token()
		return out, err
	default:
		return token, nil
	}
}
