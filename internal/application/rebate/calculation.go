// Package rebate implements the versioned hash-game differential rebate rules.
package rebate

import "errors"

var ErrInvalid = errors.New("返水参数无效")

type Ancestor struct {
	UserID       string `json:"user_id"`
	Level        int    `json:"level"`
	RatePerMille int    `json:"rate_per_mille"`
	IsVirtual    bool   `json:"is_virtual"`
}

type Allocation struct {
	Ancestor
	BaseMinor            int64 `json:"base_minor"`
	DifferentialPerMille int   `json:"differential_per_mille"`
	AmountMinor          int64 `json:"amount_minor"`
}

// Calculate uses stake for road/guess and positive net winnings for dodge.
// Each recipient's differential is rounded down independently.
func Calculate(stake, payout int64, mode string, chain []Ancestor) ([]Allocation, error) {
	if stake < 0 || payout < 0 {
		return nil, ErrInvalid
	}
	switch mode {
	case "road", "guess", "dodge":
	default:
		return nil, ErrInvalid
	}
	base := stake
	if mode == "dodge" {
		base = 0
		if payout > stake {
			base = payout - stake
		}
	}
	var result []Allocation
	level, rate := 0, 0
	for _, a := range chain {
		if a.Level < 0 || a.Level > 6 || a.RatePerMille < 0 || a.RatePerMille > 1000 {
			return nil, ErrInvalid
		}
		if a.IsVirtual || a.Level <= level || a.RatePerMille <= rate {
			continue
		}
		delta := a.RatePerMille - rate
		amount := base/1000*int64(delta) + base%1000*int64(delta)/1000
		level, rate = a.Level, a.RatePerMille
		if amount > 0 {
			result = append(result, Allocation{Ancestor: a, BaseMinor: base, DifferentialPerMille: delta, AmountMinor: amount})
		}
	}
	return result, nil
}
