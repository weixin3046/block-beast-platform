package realtime

import (
	"net/http/httptest"
	"testing"
)

func TestEventTargetSeparatesPublicAndPrivateEvents(t *testing.T) {
	if userID, broadcast := eventTarget("game.round.settled", []byte(`{}`)); userID != "" || !broadcast {
		t.Fatalf("game event target = %q, %v", userID, broadcast)
	}
	if userID, broadcast := eventTarget("wallet.withdrawal.requested", []byte(`{"user_id":"u1"}`)); userID != "u1" || broadcast {
		t.Fatalf("wallet event target = %q, %v", userID, broadcast)
	}
	if userID, broadcast := eventTarget("wallet.ledger.committed", []byte(`{}`)); userID != "" || broadcast {
		t.Fatalf("unscoped private event must not be delivered")
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
