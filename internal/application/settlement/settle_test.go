package settlement

import (
	"encoding/json"
	"testing"
)

func TestHashSelectionWins(t *testing.T) {
	outcome := []string{"5", "big", "odd"}
	for _, test := range []struct {
		mode string
		pick string
		want bool
	}{
		{mode: "guess", pick: "5", want: true},
		{mode: "guess", pick: "4", want: false},
		{mode: "dodge", pick: "4", want: true},
		{mode: "dodge", pick: "5", want: false},
		{mode: "road", pick: "big", want: true},
		{mode: "road", pick: "even", want: false},
	} {
		raw, _ := json.Marshal(map[string]string{"pick": test.pick})
		if got := hashSelectionWins(test.mode, raw, outcome); got != test.want {
			t.Fatalf("hashSelectionWins(%q,%q) = %v, want %v", test.mode, test.pick, got, test.want)
		}
	}
}

func TestWithinPoolRejectsValuesOutsideThePool(t *testing.T) {
	pool := []string{"red", "black"}
	if !withinPool([]string{"red"}, pool) {
		t.Fatal("outcome inside the pool should be accepted")
	}
	if withinPool([]string{"red", "blue"}, pool) {
		t.Fatal("outcome with values outside the pool must be rejected")
	}
	// 空 outcome 由 SettleRound 的 ErrInvalidOutcome 前置校验拦截，withinPool 不做重复检查。
}

func TestSettlementInputValidation(t *testing.T) {
	if !containsEmpty([]string{"red", ""}) {
		t.Fatal("empty outcome should be rejected")
	}
	if sameStrings([]string{"red", "blue"}, []string{"red", "blue"}) != true {
		t.Fatal("identical outcomes should match")
	}
	if sameStrings([]string{"red"}, []string{"blue"}) {
		t.Fatal("different outcomes must not match")
	}
}
