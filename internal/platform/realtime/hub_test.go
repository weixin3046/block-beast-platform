package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/coder/websocket"
)

func TestEventTargetSeparatesPublicAndPrivateEvents(t *testing.T) {
	if userIDs, broadcast := eventTargets("game.round.settled", []byte(`{}`)); len(userIDs) != 0 || !broadcast {
		t.Fatalf("game event target = %q, %v", userIDs, broadcast)
	}
	if userIDs, broadcast := eventTargets("wallet.withdrawal.requested", []byte(`{"user_id":"u1"}`)); len(userIDs) != 1 || userIDs[0] != "u1" || broadcast {
		t.Fatalf("wallet event target = %q, %v", userIDs, broadcast)
	}
	if userIDs, broadcast := eventTargets("wallet.ledger.committed", []byte(`{}`)); len(userIDs) != 0 || broadcast {
		t.Fatalf("unscoped private event must not be delivered")
	}
	if userIDs, broadcast := eventTargets("chat.message.created", []byte(`{"user_ids":["u1","u2"]}`)); len(userIDs) != 2 || broadcast {
		t.Fatalf("chat event target = %q, %v", userIDs, broadcast)
	}
}

func TestAccessTokenPrefersWebSocketSubprotocol(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/ws?access_token=query-token", nil)
	request.Header.Set("Sec-WebSocket-Protocol", "bearer.jwt-token")
	token, protocol := accessToken(request)
	if token != "jwt-token" || protocol != "bearer.jwt-token" {
		t.Fatalf("accessToken = %q, %q", token, protocol)
	}
}

func TestDecodeCommandValidatesVersionAndTopics(t *testing.T) {
	roundID := "f39ac19d-20a0-42d7-a876-87aa3618635e"
	command, err := decodeCommand([]byte(`{"v":1,"type":"subscribe","topics":["game","round:` + roundID + `"],"request_id":"r1"}`))
	if err != nil {
		t.Fatalf("decode valid command: %v", err)
	}
	if command.RequestID != "r1" || len(command.Topics) != 2 {
		t.Fatalf("command = %#v", command)
	}
	for _, payload := range [][]byte{
		[]byte(`{"v":2,"type":"ping"}`),
		[]byte(`{"v":1,"type":"subscribe","topics":[]}`),
		[]byte(`{"v":1,"type":"subscribe","topics":["wallet"]}`),
		[]byte(`{"v":1,"type":"subscribe","topics":["round:not-a-uuid"]}`),
	} {
		if _, err := decodeCommand(payload); !errors.Is(err, errInvalidCommand) {
			t.Fatalf("payload %s error = %v, want errInvalidCommand", payload, err)
		}
	}
}

func TestEventTopicsIncludesRoundSubscription(t *testing.T) {
	roundID := "f39ac19d-20a0-42d7-a876-87aa3618635e"
	topics := eventTopics("game.round.settled", []byte(`{"round_id":"`+roundID+`"}`))
	if len(topics) != 2 || topics[0] != "game" || topics[1] != "round:"+roundID {
		t.Fatalf("topics = %#v", topics)
	}
	if topics := eventTopics("wallet.ledger.committed", []byte(`{}`)); topics != nil {
		t.Fatalf("private event topics = %#v, want nil", topics)
	}
}

func TestWebSocketHandshakeAndSubscription(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	token, err := identity.IssueAccessToken([]byte(secret), "user-1", []string{identity.RolePlayer}, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHub(secret, []string{"*"}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?access_token="+token, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test complete")

	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read hello: %v", err)
	}
	var hello serverMessage
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Type != "hello" || hello.Version != ProtocolVersion {
		t.Fatalf("hello = %s, error = %v", payload, err)
	}

	command := []byte(`{"v":1,"type":"unsubscribe","topics":["game"],"request_id":"request-1"}`)
	if err := connection.Write(ctx, websocket.MessageText, command); err != nil {
		t.Fatalf("write command: %v", err)
	}
	_, payload, err = connection.Read(ctx)
	if err != nil {
		t.Fatalf("read subscription acknowledgement: %v", err)
	}
	var acknowledgement serverMessage
	if err := json.Unmarshal(payload, &acknowledgement); err != nil || acknowledgement.Type != "subscribed" || acknowledgement.RequestID != "request-1" || len(acknowledgement.Topics) != 0 {
		t.Fatalf("acknowledgement = %s, error = %v", payload, err)
	}
}
