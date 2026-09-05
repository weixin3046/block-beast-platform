package credit

import (
	"math"
	"testing"
)

func TestSpinDisplayPrecision(t *testing.T) {
	r := SpinResult{CostCurrency: "USDT", RewardCurrency: "USDT", CostDecimals: 6, RewardDecimals: 6, CostMinor: 1000000000, RewardMinor: 1985000000, CostBalanceAfterSpin: 1985123456, RewardBalance: 1985123456}
	if err := formatSpinValues(&r); err != nil || r.Cost != "1000.000000" || r.Reward != "1985.000000" || r.CostAvailable != "1985.123456" {
		t.Fatal(r, err)
	}
}
func TestPrizeWeightBounds(t *testing.T) {
	p := []SpinPrize{{ID: "1", Label: "a", Currency: "USDT", AmountMinor: 1, Weight: math.MaxInt64}, {ID: "2", Label: "b", Currency: "USDT", AmountMinor: 1, Weight: 1}}
	if _, ok := choosePrize(p); ok {
		t.Fatal("overflow accepted")
	}
	p[0].Weight = 1
	p[1].Weight = 0
	if _, ok := choosePrize(p); ok {
		t.Fatal("zero weight accepted")
	}
}
