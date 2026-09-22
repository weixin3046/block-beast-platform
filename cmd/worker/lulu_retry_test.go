package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryLuluEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	attempts := 0
	if !retryLuluEvent(ctx, time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary database error")
		}
		return nil
	}, func(error) {}) || attempts != 3 {
		t.Fatalf("attempts=%d", attempts)
	}
	cancel()
	if retryLuluEvent(ctx, time.Millisecond, func() error { t.Fatal("called after cancellation"); return nil }, func(error) {}) {
		t.Fatal("canceled retry succeeded")
	}
}

func TestLuluPublishContextDrainsAfterSubscriptionStops(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	ctx, cancel := luluPublishContext(parent, 50*time.Millisecond)
	defer cancel()
	stop()
	if ctx.Err() != nil {
		t.Fatal("cancelled before drain")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown unbounded")
	}
}
