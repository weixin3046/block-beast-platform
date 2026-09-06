package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/virtualbot"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type robotStub struct {
	RobotPlanService
	input virtualbot.PlanInput
	calls int
}

func (s *robotStub) CreatePlan(_ context.Context, actor, key string, in virtualbot.PlanInput) (virtualbot.Plan, error) {
	s.input = in
	s.calls++
	return virtualbot.Plan{PlanInput: in, ID: "94000000-0000-4000-8000-000000000001", IsVirtual: true}, nil
}
func TestRobotPlanAmountsAndRoles(t *testing.T) {
	for _, role := range []string{"", "player", "operator", "admin"} {
		stub := &robotStub{}
		s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithRobotPlans(stub))
		body := `{"request_id":"create-1","user_id":100009,"enabled":true,"game_type":"hash_9","game_room_id":"94000000-0000-4000-8000-000000000001","currency":"POINTS","min_stake":1.5,"max_stake":100,"skip_min":1,"skip_max":3,"selections":[{"play_mode":"road","pick":"odd"}]}`
		r := httptest.NewRequest("POST", "/v1/admin/robot-plans", strings.NewReader(body))
		if role != "" {
			r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 201
		if role == "" {
			want = 401
		} else if role == "player" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("%s %d %s", role, w.Code, w.Body.String())
		}
		if want == 201 && (stub.input.MinStakeMinor != 1500 || stub.input.MaxStakeMinor != 100000 || strings.Contains(w.Body.String(), "_minor") || !strings.Contains(w.Body.String(), `"min_stake":"1.5"`)) {
			t.Fatal(w.Body.String(), stub.input)
		}
		if want != 201 && stub.calls != 0 {
			t.Fatal("unauthorized request reached service")
		}
	}
}
