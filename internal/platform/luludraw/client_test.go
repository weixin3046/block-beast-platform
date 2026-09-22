package luludraw

import (
	"context"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestChaseCancellationKeepsConnectionUsable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	received := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				return
			}
			received <- b
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	client := &Client{enc: make([]byte, 32), mac: make([]byte, 32)}
	chaseCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); client.chaseHistory(chaseCtx, ctx, conn, "xdy") }()
	select {
	case frame := <-received:
		if plain := client.decryptFrame(frame); len(plain) != 1 || !strings.Contains(string(plain[0]), "2007") {
			t.Fatal("missing history request")
		}
	case <-ctx.Done():
		t.Fatal("no history request")
	}
	stop()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("chase did not stop")
	}
	if err = client.writeEvent(ctx, conn, "2001", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("connection unusable after chase")
	}
}

func TestInvalidRoomDoesNotBecomePartialResult(t *testing.T) {
	events := parseMessagesWithRound("xdy", []byte(`{"e":"2007","d":{"result":{"list":[{"round":42,"fail":[3,"bad"]},{"round":41,"fail":[6]}]}}}`), "")
	if len(events) != 2 || events[0].Round != "42" || !reflect.DeepEqual(events[0].Result, []string{"3"}) {
		t.Fatalf("LuluAll-compatible filtering failed: %+v", events)
	}
}

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
	event, ok = parseMessage("lh", []byte(`{"e":"2001","d":{"round":{"round_id":44}}}`))
	if !ok || event.Round != "44" || event.CloseAt != nil {
		t.Fatalf("round-only snapshot=%#v ok=%v", event, ok)
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
	event, ok = parseMessage("xdy", []byte(`{"e":"2001","d":{"result":{"roundId":43}}}`))
	if !ok || event.Round != "43" || event.CloseAt != nil {
		t.Fatalf("round-only snapshot=%#v ok=%v", event, ok)
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

func TestParseRaceRoundOnlySnapshot(t *testing.T) {
	event, ok := parseMessage("race", []byte(`{"e":"2001","d":{"round":{"round_id":10}}}`))
	if !ok || event.Round != "10" || event.CloseAt != nil {
		t.Fatalf("round-only snapshot=%#v ok=%v", event, ok)
	}
}

func TestParseRaceWinnerUsesCachedRound(t *testing.T) {
	event, ok := parseMessageWithRound("race", []byte(`{"e":"3005","d":{"race_rank_info":[{"item_id":6,"rank":1}]}}`), "42")
	if !ok || event.Round != "42" || !reflect.DeepEqual(event.Result, []string{"6"}) {
		t.Fatalf("event=%#v ok=%v", event, ok)
	}
}

func TestRaceExplicitWinnerAndRoundTracking(t *testing.T) {
	event, ok := parseMessageWithRound("race", []byte(`{"e":"3005","d":{"round_id":15621,"win_item_id":6,"race_rank_info":[]}}`), "15620")
	if !ok || event.Round != "15621" || !reflect.DeepEqual(event.Result, []string{"6"}) {
		t.Fatalf("winner lost: %+v %v", event, ok)
	}
	event, ok = parseMessage("race", []byte(`{"e":"3006","d":{"round_id":15622,"countdown":0}}`))
	if !ok || event.Round != "15622" || len(event.Result) != 0 {
		t.Fatalf("round tracking lost: %+v %v", event, ok)
	}
}

func TestStarSeaHistoryMalformedRowDoesNotDiscardOtherRounds(t *testing.T) {
	events := parseMessagesWithRound("xdy", []byte(`{"e":"2007","d":{"result":{"list":[{"round":42,"fail":[3]},{"round":"bad","fail":null},{"round":"41","fail":["6"]}]}}}`), "")
	if len(events) != 2 || events[0].Round != "42" || events[1].Round != "41" || !reflect.DeepEqual(events[1].Result, []string{"6"}) {
		t.Fatalf("history lost: %+v", events)
	}
}

func TestWriteEventCancellationDoesNotWaitForAnotherGame(t *testing.T) {
	// A canceled write must terminate without waiting for a different socket.
	client := &Client{enc: make([]byte, 32), mac: make([]byte, 32)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- client.writeEvent(ctx, nil, "2001", nil) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled write succeeded")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("canceled write blocked on another game's shared lock")
	}
}
