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

type activityManagerStub struct {
	CreditService
	TaskService
	calls int
	task  task.BetTaskConfig
}

func (s *activityManagerStub) ChangeSpinState(_ context.Context, _, _ string, _ *bool) error {
	s.calls++
	return nil
}
func (s *activityManagerStub) ChangeTaskState(_ context.Context, _, _ string, _ *bool) error {
	s.calls++
	return nil
}
func (s *activityManagerStub) ListSpinRecords(_ context.Context, _ credit.SpinRecordQuery) (credit.SpinRecordPage, error) {
	s.calls++
	return credit.SpinRecordPage{Items: []credit.SpinRecord{{Currency: "POINTS", AmountMinor: 1500, IsVirtual: true}}, Total: 1}, nil
}
func (s *activityManagerStub) ListProgress(_ context.Context, _ task.ProgressQuery) (task.ProgressPage, error) {
	s.calls++
	return task.ProgressPage{Items: []task.ProgressRecord{}, Total: 0}, nil
}
func (s *activityManagerStub) SaveBetTaskConfig(_ context.Context, in task.BetTaskConfig) (task.BetTaskConfig, error) {
	s.calls++
	s.task = in
	if len(in.Rewards) > 0 {
		in.RewardCurrency = in.Rewards[0].Currency
		in.RewardMinor = in.Rewards[0].AmountMinor
	}
	return in, nil
}
func TestActivityManagementPermissions(t *testing.T) {
	for _, item := range []struct{ method, path string }{
		{"DELETE", "/v1/admin/spins/94000000-0000-4000-8000-000000000001"},
		{"PUT", "/v1/admin/spins/94000000-0000-4000-8000-000000000001/enabled"},
		{"DELETE", "/v1/admin/tasks/bet-configs/94000000-0000-4000-8000-000000000001"},
		{"PUT", "/v1/admin/tasks/bet-configs/94000000-0000-4000-8000-000000000001/enabled"},
		{"GET", "/v1/admin/spin-records"}, {"GET", "/v1/admin/tasks/progress"},
	} {
		for _, role := range []string{"", "player", "operator", "admin"} {
			stub := &activityManagerStub{}
			sec := &securityStub{}
			s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(stub), WithTasks(stub), WithAdminSecurity(sec))
			body := `{"second_password":"secret","enabled":false}`
			r := httptest.NewRequest(item.method, item.path, strings.NewReader(body))
			if role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := 200
			if role == "" {
				want = 401
			} else if role != "admin" {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("%s %s: %d %s", role, item.path, w.Code, w.Body.String())
			}
			if want != 200 && stub.calls != 0 {
				t.Fatal("unauthorized service call")
			}
		}
	}
}
func TestTaskMultipleRewardAmounts(t *testing.T) {
	stub := &activityManagerStub{}
	s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithTasks(stub), WithAdminSecurity(&securityStub{}))
	body := `{"second_password":"secret","title":"task","period_type":"daily","enabled":true,"accumulation_currency":"POINTS","threshold":100,"rewards":[{"currency":"POINTS","amount":1.5},{"currency":"USDT","amount":0.1}]}`
	r := httptest.NewRequest("POST", "/v1/admin/tasks/bet-configs", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{"admin"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 201 || stub.task.ThresholdMinor != 100000 || stub.task.Rewards[0].AmountMinor != 1500 || stub.task.Rewards[1].AmountMinor != 100000 || stub.task.MaxCompleteCount != 1 || strings.Contains(w.Body.String(), "_minor") {
		t.Fatalf("%d %s %+v", w.Code, w.Body.String(), stub.task)
	}
}
func TestPublicSpinRecordsOnlyExposeDisplayAmounts(t *testing.T) {
	stub := &activityManagerStub{}
	s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithCredits(stub))
	r := httptest.NewRequest("GET", "/v1/activities/spin-records", nil)
	r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "actor", []string{"player"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"amount":"1.5"`) || !strings.Contains(w.Body.String(), `"is_virtual":true`) || strings.Contains(w.Body.String(), "balance") {
		t.Fatal(w.Code, w.Body.String())
	}
}
