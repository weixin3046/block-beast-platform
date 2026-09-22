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

func TestPermanentSessionStillChecksRevocation(t *testing.T) {
	validator := &switchSessionValidator{}
	hub := NewHub("0123456789abcdef0123456789abcdef", nil).WithSessionValidator(validator)
	defer hub.Close()
	claims := identity.AccessTokenClaims{Subject: "user", SessionID: "session"}
	if !hub.validSession(context.Background(), claims) {
		t.Fatal("permanent session rejected")
	}
	validator.revoked.Store(true)
	if hub.validSession(context.Background(), claims) {
		t.Fatal("revoked permanent session accepted")
	}
}

func (s *switchSessionValidator) ValidateSession(context.Context, identity.AccessTokenClaims) error {
	if s.revoked.Load() {
		return identity.ErrInvalidAccessToken
	}
	return nil
}

func TestRevokedSessionClosesSocketAndRejectsReconnect(t *testing.T) {
	testRevokedSessionSocket(t, time.Minute)
}

func TestPermanentRevokedSessionClosesSocketAndRejectsReconnect(t *testing.T) {
	testRevokedSessionSocket(t, 0)
}

func testRevokedSessionSocket(t *testing.T, lifetime time.Duration) {
	const secret = "0123456789abcdef0123456789abcdef"
	validator := &switchSessionValidator{}
	hub := NewHub(secret, []string{"*"}).WithSessionValidator(validator)
	server := httptest.NewServer(hub)
	defer server.Close()
	defer hub.Close()
	token, err := identity.IssueAccessToken([]byte(secret), "user", []string{"player"}, time.Now(), lifetime, "session")
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
