package natsjs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
)

func TestDrawMessageValidationAndStableID(t *testing.T) {
	event := luludraw.Event{Game: "xdy", Round: "123", Result: []string{"1"}}
	data, id, err := drawMessage(event)
	if err != nil || len(data) == 0 || id == "" {
		t.Fatal(err)
	}
	_, same, _ := drawMessage(event)
	if id != same {
		t.Fatal("unstable id")
	}
	event.Result = []string{"2"}
	_, different, _ := drawMessage(event)
	if id == different {
		t.Fatal("conflicting result deduplicated")
	}
	event.Game = "bad"
	if _, _, err := drawMessage(event); err == nil {
		t.Fatal("invalid game accepted")
	}
	if data, _, err := drawMessage(luludraw.Event{}); data != nil || err != nil {
		t.Fatal("control event persisted")
	}
}

// Run against an isolated local server: this test owns only DRAW_INBOX.
func TestDrawInboxPersistencePoisonIsolationAndReplay(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("NATS_TEST_URL is not set")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewDrawInbox(url, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { q.js.DeleteStream(drawInboxStream); q.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, round := range []string{"1", "2", "2"} {
		if err := q.Put(ctx, luludraw.Event{Game: "xdy", Round: round, Result: []string{"1"}}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := q.js.StreamInfo(drawInboxStream)
	if err != nil || info.State.Msgs != 2 {
		t.Fatalf("dedup: info=%+v err=%v", info, err)
	}
	if err := q.Put(ctx, luludraw.Event{Game: "race", Round: "3", Result: []string{"1"}}); err != nil {
		t.Fatal(err)
	}
	// Close before consuming: reconnect must retain all messages.
	q.Close()
	q, err = NewDrawInbox(url, logger)
	if err != nil {
		t.Fatal(err)
	}
	q.retryDelay = 100 * time.Millisecond
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	applied := make(chan string, 8)
	go func() {
		defer close(done)
		q.Run(runCtx, func(attempt context.Context, event luludraw.Event) error {
			if event.Round == "1" {
				<-attempt.Done()
				return errors.New("poison")
			}
			applied <- event.Round
			return nil
		})
	}()
	select {
	case got := <-applied:
		if got != "3" {
			t.Fatalf("another game was blocked: %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("slow game blocked another game")
	}
	select {
	case got := <-applied:
		if got != "2" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("poison blocked following event")
	}
	// Wait for AckSync before cancelling.
	for {
		info, err = q.js.StreamInfo(drawInboxStream)
		if err != nil {
			t.Fatal(err)
		}
		if info.State.Msgs == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("successful event not acknowledged")
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()
	<-done
	info, err = q.js.StreamInfo(drawInboxStream)
	if err != nil || info.State.Msgs != 1 {
		t.Fatalf("failed event lost: %+v %v", info, err)
	}
	q.Close()
	q, err = NewDrawInbox(url, logger)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop = context.WithCancel(ctx)
	defer stop()
	done = make(chan struct{})
	go func() {
		defer close(done)
		q.Run(runCtx, func(_ context.Context, event luludraw.Event) error { applied <- event.Round; return nil })
	}()
	select {
	case got := <-applied:
		if got != "1" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("failed event not replayed")
	}
	stop()
	<-done
}
