package task

import "testing"

func TestTaskCurrencyValidation(t *testing.T) {
	for _, currency := range []string{"USDT", "POINTS", "JADE", "ORIGIN_STONE", "CUSTOM"} {
		if !validAccumulationCurrency(currency) {
			t.Fatalf("accumulation currency %s rejected", currency)
		}
	}
	if validAccumulationCurrency("") {
		t.Fatal("empty accumulation currency accepted")
	}
	for _, currency := range []string{"STAMINA", "USDT_STAMINA", "JADE_STAMINA", "ORIGIN_STONE_STAMINA"} {
		if !validRewardCurrency(currency) {
			t.Fatalf("reward currency %s rejected", currency)
		}
	}
	if !validRewardCurrency("CUSTOM") || validRewardCurrency("") {
		t.Fatal("reward currency validation is incorrect")
	}
}
