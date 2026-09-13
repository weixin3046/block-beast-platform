package externaldraw

import (
	"context"
	"errors"
	"testing"

	"github.com/block-beast/platform/internal/platform/luludraw"
)

func TestHistoryRejectsUnknownPublicGame(t *testing.T) {
	_, err := NewHistoryReader(nil).History(context.Background(), "unknown", 10)
	if !errors.Is(err, ErrUnknownGame) {
		t.Fatalf("err = %v, want unknown game", err)
	}
}

func TestHandleRejectsInvalidEvent(t *testing.T) {
	service := NewService(nil, 3)
	err := service.Handle(context.Background(), luludraw.Event{Game: "xdy", Round: "not-a-round"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want invalid event", err)
	}
}
