package game

import (
	"encoding/json"
	"testing"
)

func TestLuluPlayAllowsOnlyConfiguredPicks(t *testing.T) {
	play := LuluPlay{Code: "up_down", Outcomes: []string{"up", "down"}}
	if !play.SelectionAllowed(json.RawMessage(`{"pick":"up"}`)) {
		t.Fatal("configured pick must be accepted")
	}
	if play.SelectionAllowed(json.RawMessage(`{"pick":"left"}`)) {
		t.Fatal("pick from another play must be rejected")
	}
}

func TestLuluPlayMapsRawRoomsBeforeEvaluatingDodge(t *testing.T) {
	play := LuluPlay{
		Code:      "dodge",
		Outcomes:  []string{"dodge_1", "dodge_2"},
		DodgeMode: true,
		ResultMap: map[string][]string{"1": {"dodge_1"}, "2": {"dodge_2"}},
	}
	if !play.SelectionWins(json.RawMessage(`{"pick":"1"}`), []string{"2"}) {
		t.Fatal("dodge pick absent from the drawn room must win")
	}
	if play.SelectionWins(json.RawMessage(`{"pick":"1"}`), []string{"1"}) {
		t.Fatal("dodge pick present in the drawn room must lose")
	}
	if play.SelectionWins(json.RawMessage(`{"pick":"dodge_1"}`), []string{"1"}) {
		t.Fatal("configured dodge outcome present in the drawn room must lose")
	}
}
