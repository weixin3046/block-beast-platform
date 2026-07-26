package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	chainapp "github.com/block-beast/platform/internal/application/chain"
	"github.com/block-beast/platform/internal/domain/events"
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

func TestApprovedWithdrawalRequiresConfiguredProvider(t *testing.T) {
	handler := processEvent(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	err := handler(context.Background(), events.Event{
		Type:    events.WithdrawalApproved,
		Payload: []byte(`{"withdrawal_id":"withdrawal-1"}`),
	})
	if !errors.Is(err, errWithdrawalProviderUnavailable) {
		t.Fatalf("error = %v, want errWithdrawalProviderUnavailable", err)
	}
}

func TestApprovedWithdrawalRejectsMissingID(t *testing.T) {
	handler := processEvent(slog.New(slog.NewJSONHandler(io.Discard, nil)), &chainapp.Service{})
	err := handler(context.Background(), events.Event{Type: events.WithdrawalApproved, Payload: []byte(`{}`)})
	if err == nil {
		t.Fatal("missing withdrawal_id must fail")
	}
}

func TestNotificationEventCanBeAcknowledged(t *testing.T) {
	handler := processEvent(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil)
	if err := handler(context.Background(), events.Event{Type: events.BetPlaced}); err != nil {
		t.Fatalf("notification event: %v", err)
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
