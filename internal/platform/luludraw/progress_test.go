package luludraw

import (
	"context"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRepeatedHistoryAndInvalidEventsDoNotMaskStall(t *testing.T) {
	start := time.Now()
	p := newDrawProgress(start)
	p.observe(Event{Round: "19564", Result: []string{"7"}}, start)
	for _, event := range []Event{{Round: "19564", Result: []string{"7"}}, {Round: "19563", Result: []string{"4"}}, {Round: "19565"}, {Round: "bad", Result: []string{"7"}}} {
		p.observe(event, start.Add(2*time.Minute))
	}
	if !p.stale(start.Add(3*time.Minute), 3*time.Minute) {
		t.Fatal("repeated or unusable events hid stalled feed")
	}
	closeAt := start.Add(4 * time.Minute)
	p.observe(Event{Round: "19565", CloseAt: &closeAt}, start.Add(3*time.Minute))
	if !p.stale(start.Add(4*time.Minute), 3*time.Minute) {
		t.Fatal("snapshot concealed missing results")
	}
	p.observe(Event{Round: "19565", Result: []string{"1"}}, start.Add(4*time.Minute))
	if p.stale(start.Add(4*time.Minute), 3*time.Minute) {
		t.Fatal("same-round result did not restore independent result progress")
	}
}
func TestWatchdogClosesLiveSocketWithNoDrawProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	accepted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		close(accepted)
		// Reading answers transport Ping automatically, but sends no draw events.
		for {
			if _, _, err = conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	<-accepted
	readDone := make(chan error, 1)
	go func() { _, _, err := conn.Read(ctx); readDone <- err }()
	if err = conn.Ping(ctx); err != nil {
		t.Fatalf("transport not healthy: %v", err)
	}
	reported := make(chan string, 1)
	client := (&Client{}).WithErrorHandler(func(game, op string, err error) { reported <- game + ":" + op })
	progress := newDrawProgress(time.Now().Add(-time.Minute))
	done := make(chan struct{})
	go func() {
		defer close(done)
		client.watchProgress(ctx, conn, "xdy", progress, 5*time.Millisecond, 30*time.Second)
	}()
	select {
	case got := <-reported:
		if got != "xdy:stalled" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("watchdog did not report")
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read remained open")
		}
	case <-ctx.Done():
		t.Fatal("watchdog did not close socket")
	}
	<-done
}
func TestWatchdogCancellationDoesNotReportFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := (&Client{}).WithErrorHandler(func(string, string, error) { t.Error("reported intentional cancellation") })
	client.watchProgress(ctx, nil, "xdy", newDrawProgress(time.Now()), time.Millisecond, time.Minute)
}
