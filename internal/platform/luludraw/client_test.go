package luludraw

import (
	"reflect"
	"testing"
)

func TestReadLimitAllowsLuluSnapshots(t *testing.T) {
	if luluReadLimit <= 32769 {
		t.Fatalf("read limit = %d, must accommodate upstream snapshots larger than 32KiB", luluReadLimit)
	}
}

func TestParseAngryFeatherResult(t *testing.T) {
	event, ok := parseMessage("lh", []byte(`{"e":"3005","d":{"round_id":42,"win_item_id":2}}`))
	if !ok || event.Round != "42" || !reflect.DeepEqual(event.Result, []string{"2"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestAngryFeatherHistoryAndSnapshot(t *testing.T) {
	events := parseMessagesWithRound("lh", []byte(`{"e":"2011","d":{"rounds":[{"round_id":42,"win_item_id":2},{"round_id":"41","win_item_id":"1"},{"round_id":40,"win_item_id":0}]}}`), "99")
	if len(events) != 2 || events[0].ResultField != "rounds[].win_item_id" || events[0].Round != "42" || events[1].Result[0] != "1" {
		t.Fatalf("history=%+v", events)
	}
	event, ok := parseMessage("lh", []byte(`{"e":"2001","d":{"round":{"round_id":43,"status":1,"end_time":"2026-09-13 20:30:00"}}}`))
	if !ok || event.Round != "43" || event.CloseAt == nil || event.CloseAt.Format("2006-01-02T15:04:05Z07:00") != "2026-09-13T12:30:00Z" {
		t.Fatalf("snapshot=%+v ok=%v", event, ok)
	}
}

func TestParseStarSeaFailedRoom(t *testing.T) {
	event, ok := parseMessage("xdy", []byte(`{"e":"3004","d":{"roundId":8,"failedRoomId":6}}`))
	if !ok || event.ResultField != "failedRoomId" || event.Round != "8" || !reflect.DeepEqual(event.Result, []string{"6"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseStarSeaKilledRooms(t *testing.T) {
	event, ok := parseMessage("xdy", []byte(`{"e":"3005","d":{"roundId":8,"killedRooms":[2,7]}}`))
	if !ok || event.ResultField != "killedRooms" || !reflect.DeepEqual(event.Result, []string{"2", "7"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseStarSeaSnapshotAnd3001Result(t *testing.T) {
	event, ok := parseMessage("xdy", []byte(`{"e":"2001","d":{"result":{"roundId":42,"countdownEndTime":1760000000000}}}`))
	if !ok || event.Round != "42" || event.CloseAt == nil {
		t.Fatalf("snapshot=%#v ok=%v", event, ok)
	}
	event, ok = parseMessage("xdy", []byte(`{"e":"3001","d":{"roundId":42,"killedRooms":[5]}}`))
	if !ok || !reflect.DeepEqual(event.Result, []string{"5"}) {
		t.Fatalf("result=%#v ok=%v", event, ok)
	}
}

func TestParseStarSeaHistoryReturnsEveryResult(t *testing.T) {
	events := parseMessagesWithRound("xdy", []byte(`{"e":"2007","d":{"result":{"list":[{"round":42,"fail":[3]},{"round":41,"fail":[6]}]}}}`), "")
	if len(events) != 2 || events[0].ResultField != "result.list[].fail" || events[0].Round != "42" || !reflect.DeepEqual(events[1].Result, []string{"6"}) {
		t.Fatalf("events=%#v", events)
	}
}

func TestParseRaceWinner(t *testing.T) {
	event, ok := parseMessage("race", []byte(`{"e":"3005","d":{"round_id":9,"race_rank_info":[{"item_id":4,"rank":2},{"item_id":2,"rank":1}]}}`))
	if !ok || event.Round != "9" || !reflect.DeepEqual(event.Result, []string{"2"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseRaceFinalMessageKeepsResultAndCloseTime(t *testing.T) {
	event, ok := parseMessage("race", []byte(`{"e":"3006","d":{"round_id":9,"countdownEndTime":1760000000000,"race_rank_info":[{"item_id":3,"rank":1}]}}`))
	if !ok || event.CloseAt == nil || event.Round != "9" || !reflect.DeepEqual(event.Result, []string{"3"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestParseRaceWinnerUsesCachedRound(t *testing.T) {
	event, ok := parseMessageWithRound("race", []byte(`{"e":"3005","d":{"race_rank_info":[{"item_id":6,"rank":1}]}}`), "42")
	if !ok || event.Round != "42" || !reflect.DeepEqual(event.Result, []string{"6"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}
