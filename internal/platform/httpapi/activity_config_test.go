package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/task"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

type activityConfigStub struct {
	CreditService
	TaskService
	spin  credit.SpinConfig
	task  task.BetTaskConfig
	calls int
}

func (s *activityConfigStub) SaveSpinConfig(_ context.Context, v credit.SpinConfig) (credit.SpinConfig, error) {
	s.spin = v
	s.calls++
	return v, nil
}
func (s *activityConfigStub) SaveBetTaskConfig(_ context.Context, v task.BetTaskConfig) (task.BetTaskConfig, error) {
	s.task = v
	s.calls++
	return v, nil
}

func TestSingleConfigRoutes(t *testing.T) {
	for _, kind := range []string{"spins", "tasks/bet-configs"} {
		for _, method := range []string{"POST", "PUT"} {
			for _, role := range []string{"", "player", "operator", "admin"} {
				stub := &activityConfigStub{}
				s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(stub), WithTasks(stub), WithAdminSecurity(&securityStub{}))
				body := `{"second_password":"secret","title":"转盘","enabled":true,"sort_order":0,"cost_currency":"POINTS","cost":1.5,"prizes":[{"label":"奖项","currency":"USDT","amount":2,"weight":1}]}`
				if kind != "spins" {
					body = `{"second_password":"secret","enabled":true,"accumulation_currency":"POINTS","threshold":100,"reward_currency":"USDT","reward":1.5}`
				}
				path := "/v1/admin/" + kind
				if method == "PUT" {
					path += "/94000000-0000-4000-8000-000000000001"
				}
				want := 201
				if method == "PUT" {
					want = 200
				}
				if role == "" {
					want = 401
				} else if role != "admin" {
					want = 403
				}
				for _, bad := range []bool{false, true} {
					b := body
					if bad {
						b = strings.TrimSuffix(body, "}") + `,"code":"frontend-defined"}`
					}
					r := httptest.NewRequest(method, path, strings.NewReader(b))
					if role != "" {
						r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
					}
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					expected := want
					if bad && role == "admin" {
						expected = 400
					}
					if w.Code != expected {
						t.Fatalf("%s %s %s: %d %s", method, kind, role, w.Code, w.Body.String())
					}
				}
				if role == "admin" {
					if stub.calls != 1 {
						t.Fatal("invalid body reached service")
					}
					if kind == "spins" && (stub.spin.CostMinor != 1500 || stub.spin.Prizes[0].AmountMinor != 2000000) {
						t.Fatal("spin amount conversion")
					}
					if kind != "spins" && (stub.task.ThresholdMinor != 100000 || stub.task.RewardMinor != 1500000) {
						t.Fatal("task amount conversion")
					}
				} else if stub.calls != 0 {
					t.Fatal("unauthorized service call")
				}
			}
		}
	}
}
