package main

import (
	"context"
	app "github.com/block-beast/platform/internal/application/lulu"
	"log/slog"
	"time"
)

// Reload credentials without allowing old and new subscriptions to overlap.
func runLuluDrawReload(ctx context.Context, ticks <-chan time.Time, load func(context.Context) (app.RuntimeConfig, error), build func(app.RuntimeConfig) (func(context.Context), error), logger *slog.Logger) {
	var active app.RuntimeConfig
	var cancel context.CancelFunc
	var done chan struct{}
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
			done = nil
		}
	}
	defer stop()
	refresh := func() {
		readCtx, release := context.WithTimeout(ctx, 5*time.Second)
		current, err := load(readCtx)
		release()
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("Lulu draw configuration reload failed")
			}
			return
		}
		if !current.Enabled {
			stop()
			return
		}
		if cancel != nil && current.Token == active.Token && current.ReceiverUID == active.ReceiverUID && current.ProtocolKey == active.ProtocolKey {
			return
		}
		run, err := build(current)
		if err != nil {
			stop()
			logger.Warn("Lulu draw subscription configuration is invalid")
			return
		}
		stop()
		if ctx.Err() != nil {
			return
		}
		active = current
		subscriptionCtx, subscriptionCancel := context.WithCancel(ctx)
		cancel = subscriptionCancel
		done = make(chan struct{})
		finished := done
		go func() { defer close(finished); run(subscriptionCtx) }()
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			cancel()
			cancel = nil
			done = nil
		case <-ticks:
			refresh()
		}
	}
}
