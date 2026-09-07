package realtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/coder/websocket"
)

type switchSessionValidator struct{ revoked atomic.Bool }

func (s *switchSessionValidator) ValidateSession(context.Context, identity.AccessTokenClaims) error {
	if s.revoked.Load() {
		return identity.ErrInvalidAccessToken
	}
	return nil
}

func TestRevokedSessionClosesSocketAndRejectsReconnect(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	validator := &switchSessionValidator{}
	hub := NewHub(secret, []string{"*"}).WithSessionValidator(validator)
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	token, err := identity.IssueAccessToken([]byte(secret), "user", []string{"player"}, time.Now(), time.Minute, "session")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "?access_token=" + token
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	validator.revoked.Store(true)
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("close error=%v", err)
	}
	if conn, response, err := websocket.Dial(ctx, url, nil); err == nil {
		conn.CloseNow()
		t.Fatal("revoked reconnect accepted")
	} else if response == nil || response.StatusCode != 401 {
		t.Fatalf("reconnect=%v %v", response, err)
	}
}
