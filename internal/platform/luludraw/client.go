// Package luludraw normalizes Lulu draw messages without retaining player data.
package luludraw

import (
	"encoding/json"
	"strconv"
	"time"
)

type Event struct {
	Game    string
	Kind    string
	Round   string
	CloseAt *time.Time
	Result  []string
}

type envelope struct {
	Event string          `json:"e"`
	Data  json.RawMessage `json:"d"`
}

func parseMessage(game string, raw []byte) (Event, bool) {
	var message envelope
	if json.Unmarshal(raw, &message) != nil {
		return Event{}, false
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(message.Data, &data) != nil {
		return Event{}, false
	}
	round := rawRound(data)
	event := Event{Game: game, Kind: message.Event, Round: round}
	switch game {
	case "lh":
		if message.Event == "3005" {
			if value, ok := rawInt(data, "win_item_id", "winItemId", "winner_camp"); ok && (value == 1 || value == 2) && round != "" {
				event.Result = []string{strconv.FormatInt(value, 10)}
				return event, true
			}
		}
	case "xdy":
		if message.Event == "3004" || message.Event == "3005" {
			if value, ok := rawInt(data, "failedRoomId"); ok && value >= 1 && value <= 8 && round != "" {
				event.Result = []string{strconv.FormatInt(value, 10)}
				return event, true
			}
			if value, ok := data["killedRooms"]; ok && round != "" {
				var rooms []int64
				if json.Unmarshal(value, &rooms) == nil {
					for _, room := range rooms {
						if room >= 1 && room <= 8 {
							event.Result = append(event.Result, strconv.FormatInt(room, 10))
						}
					}
					if len(event.Result) > 0 {
						return event, true
					}
				}
			}
		}
	case "race":
		if message.Event == "3005" && round != "" {
			var ranks []struct {
				ItemID int64 `json:"item_id"`
				Rank   int64 `json:"rank"`
			}
			if value, ok := data["race_rank_info"]; ok && json.Unmarshal(value, &ranks) == nil {
				for _, rank := range ranks {
					if rank.Rank == 1 && rank.ItemID >= 1 && rank.ItemID <= 6 {
						event.Result = []string{strconv.FormatInt(rank.ItemID, 10)}
						return event, true
					}
				}
			}
		}
	}
	return Event{}, false
}

func rawRound(data map[string]json.RawMessage) string {
	value, ok := rawInt(data, "round_id", "roundId")
	if !ok || value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func rawInt(data map[string]json.RawMessage, names ...string) (int64, bool) {
	for _, name := range names {
		value, ok := data[name]
		if !ok {
			continue
		}
		var number int64
		if json.Unmarshal(value, &number) == nil {
			return number, true
		}
		var text string
		if json.Unmarshal(value, &text) == nil {
			if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}
