package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/block-beast/platform/internal/domain/game"
)

func TestLuluOutcomeRejectsInvalidRules(t *testing.T) {
	source := NewLuluResultSource(nil)
	_, err := source.Outcome(context.Background(), game.Round{Sequence: 42}, game.Rules{Source: "lulu_ws"})
	if !errors.Is(err, game.ErrInvalidRules) {
		t.Fatalf("err = %v, want invalid rules", err)
	}
}

func TestLuluOutcomeKeepsRawRoomsForSharedGame(t *testing.T) {
	rules, err := game.ParseRules(json.RawMessage(`{"outcomes":["1","2"],"payout_multiplier":1,"source":"lulu_ws","extras":{"external_game":"lh","lulu_shared":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := luluOutcomeForRules(rules, []string{"2"})
	if err != nil || len(got) != 1 || got[0] != "2" {
		t.Fatalf("outcome = %#v, %v", got, err)
	}
}
