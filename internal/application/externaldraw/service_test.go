package externaldraw

import (
	"context"
	"errors"
	"testing"

	"github.com/block-beast/platform/internal/platform/luludraw"
)

func TestHandleRejectsInvalidEvent(t *testing.T) {
	service := NewService(nil, 3)
	err := service.Handle(context.Background(), luludraw.Event{Game: "xdy", Round: "not-a-round"})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("err = %v, want invalid event", err)
	}
}
