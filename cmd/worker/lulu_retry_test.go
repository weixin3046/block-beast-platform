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
