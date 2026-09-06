package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
)

type virtualCreationStub struct {
	AnalyticsService
	calls int
}

func (s *virtualCreationStub) CreateVirtualAccount(_ context.Context, in operations.VirtualAccountInput) (operations.VirtualAccount, error) {
	s.calls++
	if in.ActorUserID != "actor" || in.AvatarURL != "uploads/avatar.png" {
		return operations.VirtualAccount{}, operations.ErrInvalidAvatar
	}
	return operations.VirtualAccount{UserID: 100009, LoginName: in.LoginName, DisplayName: "用户100009", UserStatus: "active", AvatarURL: "/v1/avatars/100009", Currency: "POINTS", IntervalSeconds: 60}, nil
}
func TestVirtualCreationContractAndRoles(t *testing.T) {
	for _, role := range []string{"", "player", "operator", "admin"} {
		stub := &virtualCreationStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAnalytics(stub))
		r := httptest.NewRequest("POST", "/v1/admin/virtual-accounts", strings.NewReader(`{"login_name":"robot-demo","password":"test-password-123","avatar_url":"uploads/avatar.png"}`))
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 201
		if role == "" {
			want = 401
		}
		if role == "player" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", role, w.Code, w.Body.String())
		}
		if want != 201 {
			if stub.calls != 0 {
				t.Fatal("unauthorized creation")
			}
			continue
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 6 || got["is_virtual"] != true || got["avatar_url"] != "/v1/avatars/100009" || got["status"] != "active" {
			t.Fatal(got)
		}
	}
}
