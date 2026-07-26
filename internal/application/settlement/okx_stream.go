package settlement

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const okxCandleChannel = "candle1m"

type klineStream interface {
	Subscribe(symbol string)
	ClosePrice(symbol string, minute time.Time) (string, bool)
	Close()
}

// OkxKlineStream maintains one public OKX business WebSocket connection.
// Confirmed candles are cached for settlement; REST remains the recovery path.
type OkxKlineStream struct {
	url    string
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	mu            sync.RWMutex
	connection    *websocket.Conn
	subscriptions map[string]struct{}
	candles       map[string]map[int64]string
	writeMu       sync.Mutex
}

func NewOkxKlineStream(url string) *OkxKlineStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &OkxKlineStream{
		url: url, ctx: ctx, cancel: cancel,
		subscriptions: make(map[string]struct{}),
		candles:       make(map[string]map[int64]string),
	}
}

func (stream *OkxKlineStream) Subscribe(symbol string) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" || stream.url == "" {
		return
	}
	stream.mu.Lock()
	_, existed := stream.subscriptions[symbol]
	stream.subscriptions[symbol] = struct{}{}
	connection := stream.connection
	stream.mu.Unlock()
	stream.once.Do(func() { go stream.run() })
	if !existed && connection != nil {
		_ = stream.writeSubscription(connection, symbol)
	}
}

func (stream *OkxKlineStream) ClosePrice(symbol string, minute time.Time) (string, bool) {
	stream.mu.RLock()
	defer stream.mu.RUnlock()
	price, ok := stream.candles[strings.ToUpper(symbol)][minute.UTC().Truncate(time.Minute).UnixMilli()]
	return price, ok
}

func (stream *OkxKlineStream) Close() {
	stream.cancel()
	stream.mu.Lock()
	connection := stream.connection
	stream.connection = nil
	stream.mu.Unlock()
	if connection != nil {
		_ = connection.Close(websocket.StatusNormalClosure, "worker stopped")
	}
}

func (stream *OkxKlineStream) run() {
	backoff := time.Second
	for stream.ctx.Err() == nil {
		connection, _, err := websocket.Dial(stream.ctx, stream.url, nil)
		if err != nil {
			if !waitContext(stream.ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		stream.mu.Lock()
		stream.connection = connection
		symbols := make([]string, 0, len(stream.subscriptions))
		for symbol := range stream.subscriptions {
			symbols = append(symbols, symbol)
		}
		stream.mu.Unlock()
		for _, symbol := range symbols {
			if err := stream.writeSubscription(connection, symbol); err != nil {
				break
			}
		}
		heartbeatDone := make(chan struct{})
		go stream.heartbeat(connection, heartbeatDone)
		stream.read(connection)
		close(heartbeatDone)
		stream.mu.Lock()
		if stream.connection == connection {
			stream.connection = nil
		}
		stream.mu.Unlock()
		connection.CloseNow()
	}
}

func (stream *OkxKlineStream) heartbeat(connection *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stream.ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			stream.writeMu.Lock()
			err := connection.Write(stream.ctx, websocket.MessageText, []byte("ping"))
			stream.writeMu.Unlock()
			if err != nil {
				connection.CloseNow()
				return
			}
		}
	}
}

func (stream *OkxKlineStream) writeSubscription(connection *websocket.Conn, symbol string) error {
	payload, err := json.Marshal(struct {
		Op   string `json:"op"`
		Args []struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		} `json:"args"`
	}{
		Op: "subscribe",
		Args: []struct {
			Channel string `json:"channel"`
			InstID  string `json:"instId"`
		}{{Channel: okxCandleChannel, InstID: symbol}},
	})
	if err != nil {
		return err
	}
	stream.writeMu.Lock()
	defer stream.writeMu.Unlock()
	return connection.Write(stream.ctx, websocket.MessageText, payload)
}

func (stream *OkxKlineStream) read(connection *websocket.Conn) {
	for stream.ctx.Err() == nil {
		_, payload, err := connection.Read(stream.ctx)
		if err != nil {
			return
		}
		var message struct {
			Arg struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"arg"`
			Data [][]string `json:"data"`
		}
		if json.Unmarshal(payload, &message) != nil || message.Arg.Channel != okxCandleChannel {
			continue
		}
		for _, candle := range message.Data {
			if len(candle) < 9 || candle[8] != "1" || candle[4] == "" {
				continue
			}
			timestamp, err := parseOkxTimestamp(candle[0])
			if err != nil {
				continue
			}
			stream.store(message.Arg.InstID, timestamp, candle[4])
		}
	}
}

func (stream *OkxKlineStream) store(symbol string, timestamp int64, closePrice string) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	symbol = strings.ToUpper(symbol)
	if stream.candles[symbol] == nil {
		stream.candles[symbol] = make(map[int64]string)
	}
	stream.candles[symbol][timestamp] = closePrice
	cutoff := time.Now().UTC().Add(-48 * time.Hour).UnixMilli()
	for minute := range stream.candles[symbol] {
		if minute < cutoff {
			delete(stream.candles[symbol], minute)
		}
	}
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
