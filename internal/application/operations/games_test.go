package operations

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateGameType(t *testing.T) {
	validRules := json.RawMessage(`{"outcomes":["red","black"],"payout_multiplier":2}`)
	if err := validateGameType(GameTypeInput{Name: "颜色竞猜", Rules: validRules}); err != nil {
		t.Fatalf("valid game type: %v", err)
	}
	for _, input := range []GameTypeInput{
		{Name: "", Rules: validRules},
		{Name: "名称", Rules: json.RawMessage(`{"outcomes":[],"payout_multiplier":2}`)},
	} {
		if err := validateGameType(input); !errors.Is(err, ErrInvalidGameType) {
			t.Fatalf("error = %v, want ErrInvalidGameType", err)
		}
	}
}

func TestTronHashRulesIdentifiesFixedGame(t *testing.T) {
	if !tronHashRules(json.RawMessage(`{"source":"tron_hash"}`)) {
		t.Fatal("tron_hash rules must be treated as fixed")
	}
	if tronHashRules(json.RawMessage(`{"source":"okx_kline"}`)) || tronHashRules(json.RawMessage(`invalid`)) {
		t.Fatal("non-TRON or invalid rules must not be treated as fixed hash rules")
	}
}
