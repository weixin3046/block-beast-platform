package game

import (
	"encoding/json"
	"strings"
)

// LuluPlay describes one selectable play within a shared external-game round.
// The raw external rooms are mapped to this play's outcomes before win checks.
type LuluPlay struct {
	Code      string              `json:"code"`
	Outcomes  []string            `json:"outcomes"`
	ResultMap map[string][]string `json:"result_map"`
	DodgeMode bool                `json:"dodge_mode"`
}

func (play LuluPlay) SelectionAllowed(selection json.RawMessage) bool {
	var value struct {
		Pick string `json:"pick"`
	}
	if json.Unmarshal(selection, &value) != nil || value.Pick == "" {
		return false
	}
	for _, outcome := range play.Outcomes {
		if value.Pick == outcome || outcome == "dodge_"+value.Pick {
			return true
		}
	}
	return false
}

func (play LuluPlay) SelectionWins(selection json.RawMessage, rawRooms []string) bool {
	var value struct {
		Pick string `json:"pick"`
	}
	if json.Unmarshal(selection, &value) != nil || value.Pick == "" {
		return false
	}
	winning := make(map[string]struct{})
	for _, room := range rawRooms {
		for _, outcome := range play.ResultMap[room] {
			winning[outcome] = struct{}{}
		}
	}
	if play.DodgeMode {
		pick := value.Pick
		if !strings.HasPrefix(pick, "dodge_") {
			pick = "dodge_" + pick
		}
		_, hit := winning[pick]
		return !hit
	}
	_, hit := winning[value.Pick]
	return hit
}
