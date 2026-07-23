package operations

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateGameType(t *testing.T) {
	validRules := json.RawMessage(`{"outcomes":["red","black"],"payout_multiplier":2}`)
	if err := validateGameType(GameTypeInput{Code: "color_game", Name: "颜色竞猜", Rules: validRules}); err != nil {
		t.Fatalf("valid game type: %v", err)
	}
	for _, input := range []GameTypeInput{
		{Code: "Bad Code", Name: "名称", Rules: validRules},
		{Code: "valid", Name: "", Rules: validRules},
		{Code: "valid", Name: "名称", Rules: json.RawMessage(`{"outcomes":[],"payout_multiplier":2}`)},
	} {
		if err := validateGameType(input); !errors.Is(err, ErrInvalidGameType) {
			t.Fatalf("error = %v, want ErrInvalidGameType", err)
		}
	}
}
