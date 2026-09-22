package luludraw

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Transport pongs and repeated history are not evidence of a live draw feed.
// Each socket gets its own progress clock, independent of the other games.
type drawProgress struct {
	mu          sync.Mutex
	round       int64
	at          time.Time
	resultRound int64
	resultAt    time.Time
}

func newDrawProgress(now time.Time) *drawProgress { return &drawProgress{at: now, resultAt: now} }
func (p *drawProgress) observe(event Event, now time.Time) {
	if event.CloseAt == nil && len(event.Result) == 0 {
		return
	}
	round, err := strconv.ParseInt(event.Round, 10, 64)
	if err != nil || round <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if round > p.round {
		p.round = round
		p.at = now
	}
	if len(event.Result) > 0 && round > p.resultRound {
		p.resultRound = round
		p.resultAt = now
	}
}
func (p *drawProgress) stale(now time.Time, timeout time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return now.Sub(p.at) >= timeout || now.Sub(p.resultAt) >= timeout
}
func (client *Client) watchProgress(ctx context.Context, conn *websocket.Conn, game string, p *drawProgress, interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if p.stale(now, timeout) {
				client.reportError(game, "stalled", fmt.Errorf("round or result progress stalled for %s; reconnecting", timeout))
				_ = conn.CloseNow()
				return
			}
		}
	}
}
