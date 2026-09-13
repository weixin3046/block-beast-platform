package settlement

import (
	"context"
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
