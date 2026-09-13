package luludraw

import (
	"reflect"
	"testing"
)

func TestParseAngryFeatherResult(t *testing.T) {
	event, ok := parseMessage("lh", []byte(`{"e":"3005","d":{"round_id":42,"win_item_id":2}}`))
	if !ok || event.Round != "42" || !reflect.DeepEqual(event.Result, []string{"2"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseStarSeaFailedRoom(t *testing.T) {
	event, ok := parseMessage("xdy", []byte(`{"e":"3004","d":{"roundId":8,"failedRoomId":6}}`))
	if !ok || event.Round != "8" || !reflect.DeepEqual(event.Result, []string{"6"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseStarSeaKilledRooms(t *testing.T) {
	event, ok := parseMessage("xdy", []byte(`{"e":"3005","d":{"roundId":8,"killedRooms":[2,7]}}`))
	if !ok || !reflect.DeepEqual(event.Result, []string{"2", "7"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseRaceWinner(t *testing.T) {
	event, ok := parseMessage("race", []byte(`{"e":"3005","d":{"round_id":9,"race_rank_info":[{"item_id":4,"rank":2},{"item_id":2,"rank":1}]}}`))
	if !ok || event.Round != "9" || !reflect.DeepEqual(event.Result, []string{"2"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}
