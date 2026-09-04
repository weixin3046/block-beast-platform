package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
)

func TestSocketErrorsAreChinese(t *testing.T) {
	var out serverMessage
	if err := json.Unmarshal(encodeMessage(serverMessage{Type: "error", RequestID: "request-1", Error: "invalid realtime command"}), &out); err != nil {
		t.Fatal(err)
	}
	if out.Error != "实时消息指令格式不正确" || out.Type != "error" || out.RequestID != "request-1" || out.Version != 1 {
		t.Fatalf("unexpected message %+v", out)
	}
}

func TestChineseHandshakeFailures(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	hub := NewHub(string(secret), nil)
	for _, authenticated := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://localhost/v1/ws", nil)
		if authenticated {
			token, err := identity.IssueAccessToken(secret, "test-user", []string{"player"}, time.Now(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		hub.ServeHTTP(w, r)
		wantCode, want := http.StatusUnauthorized, "登录凭证缺失或无效，请重新登录"
		if authenticated {
			wantCode, want = http.StatusUpgradeRequired, "请通过 WebSocket 协议建立连接"
		}
		if w.Code != wantCode || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("authenticated=%v status=%d body=%s", authenticated, w.Code, w.Body.String())
		}
	}
}
