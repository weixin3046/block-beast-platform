package credit

import "testing"

func TestChoosePrizeSupportsEveryPlatformCurrency(t *testing.T) {
	for _, currency := range []string{CurrencyUSDT, CurrencyPoints, CurrencyJade, CurrencyOriginStone, CurrencyStamina, CurrencyUSDTStamina, CurrencyJadeStamina, CurrencyOriginStoneStamina, "CUSTOM"} {
		prize, ok := choosePrize([]SpinPrize{{ID: "p", Label: "奖品", Currency: currency, AmountMinor: 1, Weight: 1}})
		if !ok || prize.Currency != currency {
			t.Fatalf("currency %s was rejected", currency)
		}
	}
}

func TestChoosePrizeRejectsInvalidConfiguration(t *testing.T) {
	if _, ok := choosePrize([]SpinPrize{{ID: "p", Label: "奖品", Currency: "", AmountMinor: 1, Weight: 1}}); ok {
		t.Fatal("empty currency accepted")
	}
	if _, ok := choosePrize([]SpinPrize{{ID: "p", Label: "奖品", Currency: CurrencyPoints, AmountMinor: 0, Weight: 1}}); ok {
		t.Fatal("zero reward accepted")
	}
}
