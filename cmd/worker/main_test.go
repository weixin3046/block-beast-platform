package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestProcessDueRoundsClosesBatchOfOneHundred(t *testing.T) {
	repository := &recordingRoundCloser{closed: []string{"round-1", "round-2"}}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	processDueRounds(context.Background(), logger, repository)

	if repository.limit != 100 {
		t.Fatalf("close limit = %d, want 100", repository.limit)
	}
	if repository.now.IsZero() {
		t.Fatal("close time was not provided")
	}
}

func TestProcessDueRoundsHandlesRepositoryFailure(t *testing.T) {
	repository := &recordingRoundCloser{err: errors.New("database unavailable")}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	processDueRounds(context.Background(), logger, repository)

	if repository.limit != 100 {
		t.Fatalf("close limit = %d, want 100", repository.limit)
	}
}

type recordingRoundCloser struct {
	closed []string
	err    error
	now    time.Time
	limit  int
}

func (closer *recordingRoundCloser) CloseDue(_ context.Context, now time.Time, limit int) ([]string, error) {
	closer.now = now
	closer.limit = limit
	return closer.closed, closer.err
}
