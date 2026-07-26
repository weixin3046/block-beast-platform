package settlement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestOkxKlineStreamSubscribesAndCachesConfirmedCandle(t *testing.T) {
	target := time.Now().UTC().Truncate(time.Minute)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer connection.CloseNow()
		_, subscription, err := connection.Read(request.Context())
		if err != nil {
			serverErr <- err
			return
		}
		var input struct {
			Op   string `json:"op"`
			Args []struct {
				Channel string `json:"channel"`
				InstID  string `json:"instId"`
			} `json:"args"`
		}
		if err := json.Unmarshal(subscription, &input); err != nil {
			serverErr <- err
			return
		}
		if input.Op != "subscribe" || len(input.Args) != 1 ||
			input.Args[0].Channel != "candle1m" || input.Args[0].InstID != "BTC-USDT" {
			serverErr <- context.Canceled
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"arg": map[string]string{"channel": "candle1m", "instId": "BTC-USDT"},
			"data": [][]string{{
				formatInt64(target.UnixMilli()), "1", "2", "0", "65032.17", "1", "1", "1", "1",
			}},
		})
		serverErr <- connection.Write(request.Context(), websocket.MessageText, payload)
	}))
	defer server.Close()

	stream := NewOkxKlineStream("ws" + server.URL[len("http"):])
	t.Cleanup(stream.Close)
	stream.Subscribe("BTC-USDT")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if price, ok := stream.ClosePrice("BTC-USDT", target); ok {
			if price != "65032.17" {
				t.Fatalf("price = %q", price)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("server: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("confirmed candle was not cached")
}

func formatInt64(value int64) string {
	return fmt.Sprintf("%d", value)
}
