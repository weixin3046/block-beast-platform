package settlement

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
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

func TestStarSeaPrimeTimeOddEvenUsesKilledRoomCount(t *testing.T) {
	rules := game.Rules{Source: "lulu_ws", Extras: json.RawMessage(`{"lulu_shared":true,"external_game":"xdy"}`)}
	placedAt := time.Date(2026, 9, 15, 20, 30, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	for _, test := range []struct {
		pick    string
		outcome []string
		want    bool
	}{
		{pick: "odd", outcome: []string{"1", "4", "7"}, want: true},
		{pick: "even", outcome: []string{"1", "4", "7"}, want: false},
		{pick: "even", outcome: []string{"1", "4"}, want: true},
	} {
		selection, _ := json.Marshal(map[string]string{"pick": test.pick})
		got, err := selectionWins(rules, nil, "odd_even", selection, placedAt, test.outcome)
		if err != nil || got != test.want {
			t.Fatalf("pick=%s outcome=%v got=%t err=%v, want %t", test.pick, test.outcome, got, err, test.want)
		}
	}
}

func TestStarSeaOddEvenOutsidePrimeTimeUsesNormalRoomMapping(t *testing.T) {
	rules := game.Rules{Source: "lulu_ws", Extras: json.RawMessage(`{"lulu_shared":true,"external_game":"xdy"}`)}
	play := game.LuluPlay{Code: "odd_even", Outcomes: []string{"odd", "even"}, ResultMap: map[string][]string{"2": {"even"}}}
	placedAt := time.Date(2026, 9, 15, 21, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	selection := json.RawMessage(`{"pick":"even"}`)
	got, err := selectionWins(rules, map[string]game.LuluPlay{"odd_even": play}, "odd_even", selection, placedAt, []string{"2"})
	if err != nil || !got {
		t.Fatalf("outside prime time must retain normal room-parity rule: got=%t err=%v", got, err)
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
