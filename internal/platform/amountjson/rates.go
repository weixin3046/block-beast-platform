package amountjson

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/block-beast/platform/internal/domain/wallet"
)

// Hash configuration odds cross the public boundary as actual decimal rates.
// Internally they remain exact integer fractions; currency decimals do not apply.
func convertHashRates(m map[string]any, input bool) error {
	for _, mode := range []string{"guess", "dodge", "road"} {
		rateKey, numKey, denKey := mode+"_rate", mode+"_multiplier", mode+"_divisor"
		if input {
			if _, ok := m[numKey]; ok {
				return fmt.Errorf("请使用实际倍率字段 %s，不再接收 %s", rateKey, numKey)
			}
			if _, ok := m[denKey]; ok {
				return fmt.Errorf("请使用实际倍率字段 %s，不再接收 %s", rateKey, denKey)
			}
			value, ok := m[rateKey]
			if !ok {
				continue
			}
			text, err := decimalText(value)
			if err != nil {
				return err
			}
			text = strings.TrimSpace(text)
			if !wallet.ValidDisplayAmount(text) {
				return fmt.Errorf("%s 必须为大于0的十进制倍率，最多18位小数", rateKey)
			}
			r, ok := new(big.Rat).SetString(text)
			if !ok || !r.Num().IsInt64() || !r.Denom().IsInt64() {
				return fmt.Errorf("%s 超出支持范围", rateKey)
			}
			m[numKey] = json.Number(r.Num().String())
			m[denKey] = json.Number(r.Denom().String())
			delete(m, rateKey)
		} else {
			value, ok := m[numKey]
			if !ok {
				continue
			}
			ntext, err := decimalText(value)
			if err != nil {
				return err
			}
			dtext, err := decimalText(m[denKey])
			if err != nil {
				return err
			}
			n, err := json.Number(ntext).Int64()
			if err != nil {
				return err
			}
			d, err := json.Number(dtext).Int64()
			if err != nil {
				return err
			}
			if n <= 0 || d <= 0 {
				return fmt.Errorf("%s 倍率配置无效", rateKey)
			}
			r := new(big.Rat).SetFrac64(n, d)
			places, exact := r.FloatPrec()
			if !exact || places > 18 {
				return fmt.Errorf("%s 无法表示为最多18位的精确十进制倍率", rateKey)
			}
			m[rateKey] = compactMoney(r.FloatString(places))
			delete(m, numKey)
			delete(m, denKey)
		}
	}
	return nil
}
