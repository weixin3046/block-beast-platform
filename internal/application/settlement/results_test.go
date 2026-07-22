package settlement

import (
	"context"
	"testing"

	"github.com/block-beast/platform/internal/domain/game"
)

func TestHashResultSourceIsDeterministic(t *testing.T) {
	source := NewHashResultSource()
	round := game.Round{RoundID: "round-1", GameType: "beast", Sequence: 42}
	rules := game.Rules{Outcomes: []string{"red", "black", "green"}, PayoutMultiplier: 2}
	first, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("draw outcome: %v", err)
	}
	second, err := source.Outcome(context.Background(), round, rules)
	if err != nil {
		t.Fatalf("redraw outcome: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatalf("outcome must be deterministic, got %v then %v", first, second)
	}
}

func TestHashResultSourceDrawsWithinPoolWithoutDuplicates(t *testing.T) {
	source := NewHashResultSource()
	rules := game.Rules{Outcomes: []string{"1", "2", "3", "4", "5", "6"}, PayoutMultiplier: 6, ResultCount: 3}
	for sequence := int64(0); sequence < 200; sequence++ {
		round := game.Round{RoundID: "round", GameType: "dice", Sequence: sequence}
		outcome, err := source.Outcome(context.Background(), round, rules)
		if err != nil {
			t.Fatalf("draw outcome: %v", err)
		}
		if len(outcome) != 3 {
			t.Fatalf("sequence %d drew %d results, want 3", sequence, len(outcome))
		}
		seen := make(map[string]struct{}, len(outcome))
		for _, value := range outcome {
			if _, duplicated := seen[value]; duplicated {
				t.Fatalf("sequence %d drew duplicated value %q", sequence, value)
			}
			seen[value] = struct{}{}
			found := false
			for _, candidate := range rules.Outcomes {
				if candidate == value {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("sequence %d drew %q outside the pool", sequence, value)
			}
		}
	}
}

func TestHashResultSourceRejectsInvalidRules(t *testing.T) {
	source := NewHashResultSource()
	round := game.Round{RoundID: "round-1", GameType: "beast", Sequence: 1}
	if _, err := source.Outcome(context.Background(), round, game.Rules{}); err == nil {
		t.Fatal("invalid rules must be rejected")
	}
}
