package main

import (
	"context"
	"errors"
	app "github.com/block-beast/platform/internal/application/lulu"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestLuluDrawReloadCredentialsAndEnablement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	events := make(chan string, 20)
	loaded := make(chan struct{}, 20)
	var mu sync.Mutex
	current := app.RuntimeConfig{}
	var loadErr error
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runLuluDrawReload(ctx, ticks, func(context.Context) (app.RuntimeConfig, error) {
			mu.Lock()
			defer mu.Unlock()
			loaded <- struct{}{}
			return current, loadErr
		}, func(c app.RuntimeConfig) (func(context.Context), error) {
			token := c.Token
			return func(ctx context.Context) { events <- "start:" + token; <-ctx.Done(); events <- "stop:" + token }, nil
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	waitLoad := func() {
		t.Helper()
		select {
		case <-loaded:
		case <-time.After(time.Second):
			t.Fatal("configuration not loaded")
		}
	}
	waitEvent := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing %s", want)
		}
	}
	refresh := func() {
		t.Helper()
		select {
		case ticks <- time.Now():
		case <-time.After(time.Second):
			t.Fatal("refresh stalled")
		}
		waitLoad()
	}
	waitLoad()
	mu.Lock()
	current.Enabled = true
	current.Token = "old"
	mu.Unlock()
	refresh()
	waitEvent("start:old")
	// Unchanged credentials and transient read errors must not reconnect.
	refresh()
	mu.Lock()
	loadErr = errors.New("temporary database failure")
	mu.Unlock()
	refresh()
	mu.Lock()
	loadErr = nil
	current.Token = "new"
	mu.Unlock()
	refresh()
	waitEvent("stop:old")
	waitEvent("start:new")
	mu.Lock()
	current.Enabled = false
	mu.Unlock()
	refresh()
	waitEvent("stop:new")
	mu.Lock()
	current.Enabled = true
	mu.Unlock()
	refresh()
	waitEvent("start:new")
	cancel()
	waitEvent("stop:new")
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("shutdown stalled")
	}
	select {
	case unexpected := <-events:
		t.Fatalf("unexpected event %s", unexpected)
	default:
	}
}
