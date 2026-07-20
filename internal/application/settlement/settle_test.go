package settlement

import (
	"encoding/json"
	"testing"
)

func TestSelectionWinsWhenAnOutcomeValueIsSelected(t *testing.T) {
	selection := json.RawMessage(`{"color":"red","size":"large"}`)
	if !selectionWins(selection, map[string]struct{}{"red": {}}) {
		t.Fatal("selection should win when its color is in the outcome")
	}
	if selectionWins(selection, map[string]struct{}{"blue": {}}) {
		t.Fatal("selection should lose when no selected value is in the outcome")
	}
}

func TestSettlementInputValidation(t *testing.T) {
	if !containsEmpty([]string{"red", ""}) {
		t.Fatal("empty outcome should be rejected")
	}
	if !sameStrings([]string{"red", "blue"}, []string{"red", "blue"}) {
		t.Fatal("identical outcomes should match")
	}
	if sameStrings([]string{"red"}, []string{"blue"}) {
		t.Fatal("different outcomes must not match")
	}
}
