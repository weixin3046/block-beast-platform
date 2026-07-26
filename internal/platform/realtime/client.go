package realtime

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const outboundQueueSize = 128

type client struct {
	connection *websocket.Conn
	outbound   chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	topicsMu   sync.RWMutex
	topics     map[string]struct{}
}

func newClient(connection *websocket.Conn) *client {
	return &client{
		connection: connection,
		outbound:   make(chan []byte, outboundQueueSize),
		done:       make(chan struct{}),
		topics:     map[string]struct{}{"game": {}},
	}
}

func (item *client) subscribe(topics []string) {
	item.topicsMu.Lock()
	defer item.topicsMu.Unlock()
	for _, topic := range topics {
		item.topics[topic] = struct{}{}
	}
}

func (item *client) unsubscribe(topics []string) {
	item.topicsMu.Lock()
	defer item.topicsMu.Unlock()
	for _, topic := range topics {
		delete(item.topics, topic)
	}
}

func (item *client) subscribed(topic string) bool {
	item.topicsMu.RLock()
	defer item.topicsMu.RUnlock()
	_, ok := item.topics[topic]
	return ok
}

func (item *client) topicList() []string {
	item.topicsMu.RLock()
	defer item.topicsMu.RUnlock()
	topics := make([]string, 0, len(item.topics))
	for topic := range item.topics {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}

func (item *client) enqueue(payload []byte) bool {
	select {
	case item.outbound <- payload:
		return true
	default:
		item.close(websocket.StatusPolicyViolation, "client is too slow")
		return false
	}
}

func (item *client) writeLoop(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-item.done:
			return
		case payload := <-item.outbound:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := item.connection.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				item.close(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := item.connection.Ping(pingCtx)
			cancel()
			if err != nil {
				item.close(websocket.StatusGoingAway, "ping failed")
				return
			}
		}
	}
}

func (item *client) close(status websocket.StatusCode, reason string) {
	item.closeOnce.Do(func() {
		close(item.done)
		_ = item.connection.Close(status, reason)
	})
}
