package game

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseRulesValidatesRequiredFields(t *testing.T) {
	if _, err := ParseRules(json.RawMessage(`{"outcomes":["red","black"],"payout_multiplier":2}`)); err != nil {
		t.Fatalf("valid rules should parse: %v", err)
	}
	invalid := []string{
		`{}`,
		`{"outcomes":[],"payout_multiplier":2}`,
		`{"outcomes":["red"],"payout_multiplier":0}`,
		`{"outcomes":["red","red"],"payout_multiplier":2}`,
		`{"outcomes":["red",""],"payout_multiplier":2}`,
		`{"outcomes":["red","black"],"payout_multiplier":2,"result_count":3}`,
	}
	for _, raw := range invalid {
		if _, err := ParseRules(json.RawMessage(raw)); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("rules %s should be rejected, got %v", raw, err)
		}
	}
	if _, err := ParseRules(nil); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("empty rules should be rejected, got %v", err)
	}
}

func TestRulesDrawCountDefaultsToOne(t *testing.T) {
	rules := Rules{Outcomes: []string{"red", "black"}, PayoutMultiplier: 2}
	if rules.DrawCount() != 1 {
		t.Fatalf("default draw count = %d, want 1", rules.DrawCount())
	}
	rules.ResultCount = 2
	if rules.DrawCount() != 2 {
		t.Fatalf("draw count = %d, want 2", rules.DrawCount())
	}
}

func TestSelectionWinsWithoutMatchFieldUsesAnyString(t *testing.T) {
	rules := Rules{Outcomes: []string{"red", "black"}, PayoutMultiplier: 2}
	if !rules.SelectionWins(json.RawMessage(`{"color":"red","size":"large"}`), []string{"red"}) {
		t.Fatal("selection should win when any string value is in the outcome")
	}
	if rules.SelectionWins(json.RawMessage(`{"color":"blue"}`), []string{"red"}) {
		t.Fatal("selection should lose when no value is in the outcome")
	}
	if rules.SelectionWins(json.RawMessage(`not-json`), []string{"red"}) {
		t.Fatal("invalid selection JSON should lose")
	}
}

func TestSelectionWinsWithMatchFieldOnlyComparesThatField(t *testing.T) {
	rules := Rules{Outcomes: []string{"red", "large"}, PayoutMultiplier: 2, MatchField: "color"}
	selection := json.RawMessage(`{"color":"blue","size":"large"}`)
	if rules.SelectionWins(selection, []string{"large"}) {
		t.Fatal("match_field must ignore values from other fields")
	}
	if !rules.SelectionWins(selection, []string{"blue"}) {
		t.Fatal("selection should win when the matched field value is in the outcome")
	}
}

func TestSelectionWinsWithNestedMatchField(t *testing.T) {
	rules := Rules{Outcomes: []string{"red"}, PayoutMultiplier: 2, MatchField: "pick.color"}
	if !rules.SelectionWins(json.RawMessage(`{"pick":{"color":"red"},"note":"ignored"}`), []string{"red"}) {
		t.Fatal("nested match_field should resolve dot paths")
	}
	if rules.SelectionWins(json.RawMessage(`{"pick":{}}`), []string{"red"}) {
		t.Fatal("missing nested field should lose")
	}
}
