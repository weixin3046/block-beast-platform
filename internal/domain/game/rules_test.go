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

func TestParseRulesWithNewFields(t *testing.T) {
	// 新字段（source/extras/dodge_mode）应能正确解析，旧数据无这些字段也能兼容。
	raw := json.RawMessage(`{"outcomes":["0","1"],"payout_multiplier":194,"source":"tron_hash","extras":{"base_block_height":84687805},"dodge_mode":true}`)
	rules, err := ParseRules(raw)
	if err != nil {
		t.Fatalf("parse rules with new fields: %v", err)
	}
	if rules.Source != "tron_hash" {
		t.Fatalf("source = %q, want tron_hash", rules.Source)
	}
	if !rules.DodgeMode {
		t.Fatal("dodge_mode should be true")
	}
	if len(rules.Extras) == 0 {
		t.Fatal("extras should not be empty")
	}
	// 旧数据（无新字段）应正常解析。
	old := json.RawMessage(`{"outcomes":["red","black"],"payout_multiplier":2}`)
	oldRules, err := ParseRules(old)
	if err != nil {
		t.Fatalf("parse old rules: %v", err)
	}
	if oldRules.Source != "" || oldRules.DodgeMode || len(oldRules.Extras) > 0 {
		t.Fatal("old rules should have empty new fields")
	}
}

func TestSelectionWinsDodgeMode(t *testing.T) {
	rules := Rules{
		Outcomes:         []string{"dodge_0", "dodge_1", "dodge_2", "dodge_3", "dodge_4", "dodge_5", "dodge_6", "dodge_7", "dodge_8", "dodge_9"},
		PayoutMultiplier: 194,
		DodgeMode:        true,
	}
	// 躲避 3，实际尾数是 5 → outcome 是 ["dodge_5"]，"dodge_3" 不在其中 → 赢。
	if !rules.SelectionWins(json.RawMessage(`{"pick":"3"}`), []string{"dodge_5"}) {
		t.Fatal("dodge 3 with outcome dodge_5 should win")
	}
	// 躲避 3，实际尾数是 3 → outcome 是 ["dodge_3"]，"dodge_3" 在其中 → 输。
	if rules.SelectionWins(json.RawMessage(`{"pick":"3"}`), []string{"dodge_3"}) {
		t.Fatal("dodge 3 with outcome dodge_3 should lose")
	}
	// 非躲避模式不应受影响。
	normal := Rules{Outcomes: []string{"big", "odd"}, PayoutMultiplier: 195}
	if !normal.SelectionWins(json.RawMessage(`{"pick":"big"}`), []string{"big", "odd"}) {
		t.Fatal("normal mode should still work")
	}
}
