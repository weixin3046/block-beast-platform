package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
)

type client struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

type Hub struct {
	secret  []byte
	origins []string
	mu      sync.RWMutex
	clients map[string]map[*client]struct{}
	nats    *nats.Conn
}

func NewHub(secret string, origins []string) *Hub {
	return &Hub{secret: []byte(secret), origins: origins, clients: make(map[string]map[*client]struct{})}
}

func (hub *Hub) ConnectNATS(url string) error {
	connection, err := nats.Connect(url)
	if err != nil {
		return err
	}
	hub.nats = connection
	for _, subject := range []string{"game.>", "wallet.>", "chain.>"} {
		if _, err := connection.Subscribe(subject, hub.publish); err != nil {
			connection.Close()
			return err
		}
	}
	return connection.Flush()
}

func (hub *Hub) Close() {
	if hub.nats != nil {
		hub.nats.Close()
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, clients := range hub.clients {
		for item := range clients {
			_ = item.connection.Close(websocket.StatusGoingAway, "server shutdown")
		}
	}
}

func (hub *Hub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	token, protocol := accessToken(request)
	claims, err := identity.VerifyAccessToken(hub.secret, token, time.Now().UTC())
	if err != nil {
		http.Error(writer, "missing or invalid access token", http.StatusUnauthorized)
		return
	}
	options := &websocket.AcceptOptions{OriginPatterns: hub.origins}
	if protocol != "" {
		options.Subprotocols = []string{protocol}
	}
	connection, err := websocket.Accept(writer, request, options)
	if err != nil {
		return
	}
	item := &client{connection: connection}
	hub.add(claims.Subject, item)
	defer func() {
		hub.remove(claims.Subject, item)
		_ = connection.Close(websocket.StatusNormalClosure, "closed")
	}()
	for {
		if err := connection.Ping(request.Context()); err != nil {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(25 * time.Second):
		}
	}
}

func accessToken(request *http.Request) (token, protocol string) {
	if header := request.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer "), ""
	}
	for _, item := range strings.Split(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
		candidate := strings.TrimSpace(item)
		if strings.HasPrefix(candidate, "bearer.") {
			return strings.TrimPrefix(candidate, "bearer."), candidate
		}
	}
	return request.URL.Query().Get("access_token"), ""
}

func (hub *Hub) publish(message *nats.Msg) {
	envelope, err := json.Marshal(struct {
		Subject string          `json:"subject"`
		Payload json.RawMessage `json:"payload"`
	}{message.Subject, append([]byte(nil), message.Data...)})
	if err != nil {
		return
	}
	userID, broadcast := eventTarget(message.Subject, message.Data)
	if broadcast {
		hub.broadcast(envelope)
		return
	}
	if userID != "" {
		hub.send(userID, envelope)
	}
}

func eventTarget(subject string, data []byte) (userID string, broadcast bool) {
	if strings.HasPrefix(subject, "game.") {
		return "", true
	}
	var payload struct {
		UserID string `json:"user_id"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "", false
	}
	return payload.UserID, false
}

func (hub *Hub) add(userID string, item *client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.clients[userID] == nil {
		hub.clients[userID] = make(map[*client]struct{})
	}
	hub.clients[userID][item] = struct{}{}
}

func (hub *Hub) remove(userID string, item *client) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	delete(hub.clients[userID], item)
	if len(hub.clients[userID]) == 0 {
		delete(hub.clients, userID)
	}
}

func (hub *Hub) broadcast(payload []byte) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, clients := range hub.clients {
		for item := range clients {
			item.write(payload)
		}
	}
}

func (hub *Hub) send(userID string, payload []byte) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for item := range hub.clients[userID] {
		item.write(payload)
	}
}

func (item *client) write(payload []byte) {
	item.writeMu.Lock()
	defer item.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = item.connection.Write(ctx, websocket.MessageText, payload)
}
