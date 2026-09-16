package externaldraw

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
)

func TestHistoryRejectsUnknownPublicGame(t *testing.T) {
	_, err := NewHistoryReader(nil).History(context.Background(), "unknown", 10)
	if !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("err = %v, want unknown game", err)
	}
}

func TestResultRoundClosedAtOnlyBackfillsPastOrUndatedResults(t *testing.T) {
	now := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	if got, needed := resultRoundClosedAt(now, nil); !needed || !got.Equal(now) {
		t.Fatalf("undated result = %v, %v", got, needed)
	}
	past := now.Add(-time.Second)
	if got, needed := resultRoundClosedAt(now, &past); !needed || !got.Equal(past) {
		t.Fatalf("past result = %v, %v", got, needed)
	}
	future := now.Add(time.Second)
	if got, needed := resultRoundClosedAt(now, &future); needed || !got.IsZero() {
		t.Fatalf("future result = %v, %v", got, needed)
	}
}

func TestHandleRejectsInvalidEvent(t *testing.T) {
	service := NewService(nil, 3)
	err := service.Handle(context.Background(), luludraw.Event{Game: "xdy", Round: "not-a-round"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want invalid event", err)
	}
}

func TestSameDrawResultIgnoresJSONFormattingAndRoomOrder(t *testing.T) {
	for _, tc := range []struct {
		saved  string
		result []string
		want   bool
	}{
		{`["2", "7"]`, []string{"2", "7"}, true},
		{`["7", "2"]`, []string{"2", "7"}, true},
		{`["2", "7"]`, []string{"2", "8"}, false},
		{`["2", "7"]`, []string{"2", "2"}, false},
		{`null`, []string{"2"}, false},
	} {
		if got := sameDrawResult([]byte(tc.saved), tc.result); got != tc.want {
			t.Fatalf("saved=%s result=%v got=%v", tc.saved, tc.result, got)
		}
	}
}

func TestStarSeaMultiKillAuthorityDuringEveningWindow(t *testing.T) {
	closedAt := time.Date(2026, 9, 15, 20, 30, 0, 0, chinaLocation)
	multi := luludraw.Event{Game: "xdy", ResultField: "killedRooms", Result: []string{"2", "5", "7"}}
	single := luludraw.Event{Game: "xdy", ResultField: "failedRoomId", Result: []string{"5"}}

	if !isStarSeaMultiKillWindow(multi, &closedAt) {
		t.Fatal("20:00 China time must use Star Sea multi-kill authority")
	}
	if !isLowerPriorityStarSeaResult([]byte(`["2","5","7"]`), single) {
		t.Fatal("single failed room contained in the multi-kill result must be ignored")
	}
	if !isAuthoritativeStarSeaMultiKill([]byte(`["5"]`), multi) {
		t.Fatal("multi-kill result must replace an earlier contained single-room result")
	}
}

func TestStarSeaMultiKillAuthorityDoesNotApplyOutsideWindow(t *testing.T) {
	closedAt := time.Date(2026, 9, 15, 21, 0, 0, 0, chinaLocation)
	event := luludraw.Event{Game: "xdy", ResultField: "killedRooms", Result: []string{"2", "5"}}
	if isStarSeaMultiKillWindow(event, &closedAt) {
		t.Fatal("21:00 China time must not use the evening multi-kill rule")
	}
}
