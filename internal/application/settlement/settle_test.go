package settlement

import (
	"testing"
)

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
