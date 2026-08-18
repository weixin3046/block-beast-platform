package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
)

type Hub struct {
	secret  []byte
	origins []string
	mu      sync.RWMutex
	clients map[string]map[*client]struct{}
	nats    *nats.Conn
	chat    ChatSender
}

type ChatSender interface {
	SendMessage(ctx context.Context, roomID, senderUserID, clientRequestID, body string, staff bool) (chat.Message, bool, error)
}

func NewHub(secret string, origins []string) *Hub {
	return &Hub{secret: []byte(secret), origins: origins, clients: make(map[string]map[*client]struct{})}
}

func (hub *Hub) WithChatSender(sender ChatSender) *Hub {
	hub.chat = sender
	return hub
}

func (hub *Hub) ConnectNATS(url string) error {
	connection, err := nats.Connect(url)
	if err != nil {
		return err
	}
	hub.nats = connection
	for _, subject := range []string{"game.>", "wallet.>", "chain.>", "chat.>"} {
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
			item.close(websocket.StatusGoingAway, "server shutdown")
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
	item := newClient(connection)
	hub.add(claims.Subject, item)
	defer func() {
		hub.remove(claims.Subject, item)
		item.close(websocket.StatusNormalClosure, "closed")
	}()
	connectionCtx, cancel := context.WithCancel(request.Context())
	defer cancel()
	go item.writeLoop(connectionCtx)
	item.enqueue(encodeMessage(serverMessage{Type: "hello", Topics: item.topicList()}))
	for {
		messageType, payload, err := connection.Read(connectionCtx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			item.enqueue(encodeMessage(serverMessage{Type: "error", Error: "text messages are required"}))
			continue
		}
		hub.handleCommand(connectionCtx, item, claims, payload)
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
	envelope := encodeMessage(serverMessage{Type: "event", Subject: message.Subject, Payload: append([]byte(nil), message.Data...)})
	userIDs, broadcast := eventTargets(message.Subject, message.Data)
	if broadcast {
		hub.publishTopics(eventTopics(message.Subject, message.Data), envelope)
		return
	}
	for _, userID := range userIDs {
		hub.send(userID, envelope)
	}
}

func (hub *Hub) handleCommand(ctx context.Context, item *client, claims identity.AccessTokenClaims, payload []byte) {
	command, err := decodeCommand(payload)
	if err != nil {
		item.enqueue(encodeMessage(serverMessage{Type: "error", Error: err.Error()}))
		return
	}
	switch command.Type {
	case "subscribe":
		item.subscribe(command.Topics)
	case "unsubscribe":
		item.unsubscribe(command.Topics)
	case "ping":
		item.enqueue(encodeMessage(serverMessage{Type: "pong", RequestID: command.RequestID}))
		return
	case "chat.send":
		hub.sendChatMessage(ctx, item, claims, command)
		return
	}
	item.enqueue(encodeMessage(serverMessage{Type: "subscribed", RequestID: command.RequestID, Topics: item.topicList()}))
}

func (hub *Hub) sendChatMessage(ctx context.Context, item *client, claims identity.AccessTokenClaims, command clientCommand) {
	if hub.chat == nil {
		item.enqueue(encodeMessage(serverMessage{Type: "error", RequestID: command.RequestID, Error: "chat is unavailable"}))
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	message, created, err := hub.chat.SendMessage(requestCtx, command.RoomID, claims.Subject, command.RequestID, command.Body, claims.HasRole(identity.RoleAdmin, identity.RoleOperator))
	if err != nil {
		item.enqueue(encodeMessage(serverMessage{Type: "error", RequestID: command.RequestID, Error: chatCommandError(err)}))
		return
	}
	result, _ := json.Marshal(map[string]any{"message": message, "created": created})
	item.enqueue(encodeMessage(serverMessage{Type: "chat_message_sent", RequestID: command.RequestID, Payload: result}))
}

func chatCommandError(err error) string {
	switch {
	case errors.Is(err, chat.ErrInvalidMessage), errors.Is(err, chat.ErrInvalidRequestID), errors.Is(err, chat.ErrRoomAccessDenied), errors.Is(err, chat.ErrRoomNotFound):
		return err.Error()
	default:
		return "unable to send chat message"
	}
}

func eventTargets(subject string, data []byte) (userIDs []string, broadcast bool) {
	if strings.HasPrefix(subject, "game.") {
		return nil, true
	}
	var payload struct {
		UserID    string   `json:"user_id"`
		UserIDs   []string `json:"user_ids"`
		Broadcast bool     `json:"broadcast"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil, false
	}
	if strings.HasPrefix(subject, "chat.") {
		return payload.UserIDs, payload.Broadcast
	}
	if payload.UserID != "" {
		return []string{payload.UserID}, false
	}
	return nil, false
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

func (hub *Hub) publishTopics(topics []string, payload []byte) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for _, clients := range hub.clients {
		for item := range clients {
			for _, topic := range topics {
				if item.subscribed(topic) {
					item.enqueue(payload)
					break
				}
			}
		}
	}
}

func (hub *Hub) send(userID string, payload []byte) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for item := range hub.clients[userID] {
		item.enqueue(payload)
	}
}
