package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// RunLive owns publishing independently of settlement and maintenance tasks.
// Notifications are hints; the durable outbox remains the source of truth.
func (p *Processor) RunLive(ctx context.Context, dsn string, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = time.Second
	}
	var conn *pgx.Conn
	defer func() {
		if conn != nil {
			conn.Close(context.Background())
		}
	}()
	for ctx.Err() == nil {
		if conn == nil {
			connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			c, err := pgx.Connect(connectCtx, dsn)
			if err == nil {
				_, err = c.Exec(connectCtx, "LISTEN wallet_outbox_ready")
			}
			cancel()
			if err == nil {
				conn = c
			} else {
				if c != nil {
					c.Close(context.Background())
				}
				logger.Warn("outbox notification listener unavailable; polling continues")
			}
		}
		// Drain beyond one batch, including after reconnect, so coalesced
		// notifications cannot strand committed events until the next poll.
		for ctx.Err() == nil {
			n, err := p.ProcessPending(100)
			if err != nil {
				logger.Error("outbox publishing failed", "published", n, "error", err)
				break
			}
			if n < 100 {
				break
			}
		}
		waitCtx, cancel := context.WithTimeout(ctx, interval)
		if conn != nil {
			_, err := conn.WaitForNotification(waitCtx)
			if err != nil && waitCtx.Err() == nil {
				conn.Close(context.Background())
				conn = nil
			}
		} else {
			<-waitCtx.Done()
		}
		cancel()
	}
}
