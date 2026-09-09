// lulu-worker isolates Lulu polling and non-idempotent provider transfers from
// generic message retries. Run exactly one owner per receiving account.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	app "github.com/block-beast/platform/internal/application/lulu"
	"github.com/block-beast/platform/internal/config"
	provider "github.com/block-beast/platform/internal/platform/lulu"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config.Load(), logger); err != nil {
		logger.Error("Lulu worker stopped", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if cfg.PostgresDSN == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// Close rather than return a session advisory lock to the pool.
	defer func() { _ = conn.Hijack().Close(context.Background()) }()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "lulu-worker").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return errors.New("another Lulu worker owns the channel")
	}
	settings := app.NewService(pool, "").WithEncryptionKey(cfg.LuluEncryptionKey)
	recovered := false
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		// Losing the lock connection terminates the worker before another dispatch.
		if err = conn.Ping(ctx); err != nil {
			return err
		}
		if err := settings.ExpireDeposits(ctx); err != nil {
			return err
		}
		current, configErr := settings.Config(ctx)
		if configErr != nil {
			return configErr
		}
		if current.ReceiverUID != "" && !recovered {
			service := app.NewService(pool, current.ReceiverUID)
			if err = service.RecoverSending(ctx); err != nil {
				return err
			}
			recovered = true
		}
		if current.Enabled {
			runtime, runtimeErr := settings.RuntimeConfig(ctx)
			var client *provider.Client
			var clientErr error
			if runtimeErr == nil && runtime.Enabled {
				client, clientErr = provider.NewCredentialClient(runtime.APIURL, runtime.ReceiverUID, runtime.Token, runtime.ProtocolKey)
			}
			if runtimeErr != nil || !runtime.Enabled || runtime.ScanStartAt == nil || clientErr != nil {
				logger.Warn("Lulu runtime credentials or scan start are not configured")
			} else {
				service := app.NewService(pool, runtime.ReceiverUID)
				service.ConfigVersion = runtime.Version
				cycle, cancel := context.WithTimeout(ctx, 2*time.Minute)
				if err = service.Collect(cycle, client, *runtime.ScanStartAt); err != nil {
					logger.Warn("Lulu collection failed; watermark retained")
				}
				cancel()
				if err = conn.Ping(ctx); err != nil {
					return err
				}
				send, cancel := context.WithTimeout(ctx, 50*time.Second)
				if err = service.Dispatch(send, client); err != nil {
					logger.Error("Lulu dispatch persistence failed; review pending orders")
				}
				cancel()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
