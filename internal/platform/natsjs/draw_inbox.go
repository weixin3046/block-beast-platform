package natsjs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/block-beast/platform/internal/platform/luludraw"
	"github.com/nats-io/nats.go"
)

const drawInboxStream = "BLOCK_BEAST_DRAW_INBOX"

var drawGames = []string{"lh", "xdy", "race"}

// DrawInbox keeps normalized upstream events until their database transaction
// succeeds. It is separate from domain events: failed draws must never expire
// into the domain consumer's finite-retry dead-letter path.
type DrawInbox struct {
	connection    *nats.Conn
	js            nats.JetStreamContext
	subscriptions map[string]*nats.Subscription
	logger        *slog.Logger
	retryDelay    time.Duration
}

func NewDrawInbox(url string, logger *slog.Logger) (*DrawInbox, error) {
	conn, err := nats.Connect(url, nats.MaxReconnects(-1), nats.ReconnectWait(time.Second), nats.Timeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	inbox := &DrawInbox{connection: conn, logger: logger, retryDelay: 30 * time.Second, subscriptions: make(map[string]*nats.Subscription)}
	if logger == nil {
		inbox.logger = slog.Default()
	}
	if err = inbox.initialize(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initialize draw inbox: %w", err)
	}
	return inbox, nil
}

func (q *DrawInbox) initialize() error {
	var err error
	q.js, err = q.connection.JetStream(nats.MaxWait(5 * time.Second))
	if err != nil {
		return err
	}
	info, err := q.js.StreamInfo(drawInboxStream)
	if errors.Is(err, nats.ErrStreamNotFound) {
		info, err = q.js.AddStream(&nats.StreamConfig{Name: drawInboxStream, Subjects: []string{"draw.inbox.*"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, Discard: nats.DiscardNew, MaxBytes: 512 << 20, MaxAge: 0, Duplicates: 2 * time.Minute})
	}
	if err != nil {
		return err
	}
	// Refuse lossy pre-existing settings rather than silently changing retention.
	c := info.Config
	if c.Storage != nats.FileStorage || c.Retention != nats.WorkQueuePolicy || c.Discard != nats.DiscardNew || c.MaxAge != 0 || c.MaxMsgs > 0 || c.MaxMsgsPerSubject > 0 || c.MaxBytes != 512<<20 || len(c.Subjects) != 1 || c.Subjects[0] != "draw.inbox.*" {
		return errors.New("draw inbox has incompatible retention settings")
	}
	for _, game := range drawGames {
		name := "draw-inbox-" + game
		_, err = q.js.AddConsumer(drawInboxStream, &nats.ConsumerConfig{Durable: name, AckPolicy: nats.AckExplicitPolicy, AckWait: 30 * time.Second, MaxDeliver: -1, FilterSubject: "draw.inbox." + game, MaxAckPending: 4096})
		if err != nil {
			return err
		}
		q.subscriptions[game], err = q.js.PullSubscribe("draw.inbox."+game, name, nats.Bind(drawInboxStream, name))
		if err != nil {
			return err
		}
	}
	return nil
}

func drawMessage(event luludraw.Event) ([]byte, string, error) {
	if event.CloseAt == nil && len(event.Result) == 0 {
		return nil, "", nil
	}
	round, err := strconv.ParseInt(event.Round, 10, 64)
	if err != nil || round <= 0 || (event.Game != "lh" && event.Game != "xdy" && event.Game != "race") {
		return nil, "", errors.New("invalid draw inbox event")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return nil, "", err
	}
	return data, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Put only returns success after JetStream acknowledges persistence. The caller
// retries publish failures; retries and ambiguous acknowledgements are idempotent.
func (q *DrawInbox) Put(ctx context.Context, event luludraw.Event) error {
	data, id, err := drawMessage(event)
	if err != nil || data == nil {
		return err
	}
	_, err = q.js.Publish("draw.inbox."+event.Game, data, nats.MsgId(id), nats.Context(ctx))
	return err
}

func (q *DrawInbox) Close() { q.connection.Close() }

func (q *DrawInbox) Run(ctx context.Context, apply func(context.Context, luludraw.Event) error) {
	var workers sync.WaitGroup
	for game, sub := range q.subscriptions {
		workers.Add(1)
		go func() { defer workers.Done(); q.consume(ctx, game, sub, apply) }()
	}
	workers.Wait()
}

func (q *DrawInbox) consume(ctx context.Context, game string, sub *nats.Subscription, apply func(context.Context, luludraw.Event) error) {
	reportAt := time.Now().Add(time.Minute)
	lastApplied := time.Time{}
	var processed, failed uint64
	for ctx.Err() == nil {
		fetchCtx, cancel := context.WithTimeout(ctx, time.Second)
		messages, err := sub.Fetch(1, nats.Context(fetchCtx))
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrTimeout) && ctx.Err() == nil {
			q.logger.Error("Lulu draw inbox fetch failed", "game", game, "error", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		for _, msg := range messages {
			var event luludraw.Event
			err = json.Unmarshal(msg.Data, &event)
			if err == nil && event.Game != game {
				err = errors.New("draw inbox subject mismatch")
			}
			if err == nil {
				attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
				err = apply(attempt, event)
				cancel()
			}
			if err != nil {
				failed++
				q.logger.Error("Lulu draw inbox apply failed; retained for retry", "game", game, "round", event.Round, "error", err)
				if nakErr := msg.NakWithDelay(q.retryDelay); nakErr != nil {
					q.logger.Error("Lulu draw inbox retry failed", "game", game, "error", nakErr)
				}
				continue
			}
			ackCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err = msg.AckSync(nats.Context(ackCtx))
			cancel()
			if err != nil {
				q.logger.Error("Lulu draw inbox ack failed; safe to replay", "game", game, "error", err)
			} else {
				processed++
				lastApplied = time.Now()
			}
		}
		if time.Now().After(reportAt) {
			q.report(game, sub, lastApplied, processed, failed)
			reportAt = time.Now().Add(time.Minute)
		}
	}
}

func (q *DrawInbox) report(game string, sub *nats.Subscription, lastApplied time.Time, processed, failed uint64) {
	info, err := sub.ConsumerInfo()
	if err != nil {
		q.logger.Error("Lulu draw inbox status failed", "game", game, "error", err)
		return
	}
	attrs := []any{"game", game, "pending", info.NumPending, "unacked", info.NumAckPending, "processed", processed, "failed", failed, "last_applied", lastApplied}
	if info.NumPending > 100 || info.NumAckPending > 0 || lastApplied.IsZero() || time.Since(lastApplied) > 3*time.Minute {
		q.logger.Warn("Lulu draw inbox progress", attrs...)
	} else {
		q.logger.Info("Lulu draw inbox progress", attrs...)
	}
}
