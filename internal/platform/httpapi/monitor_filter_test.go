package httpapi

import (
	"context"
	"github.com/block-beast/platform/internal/application/operations"
	"net/http/httptest"
	"testing"
)

type monitorFilterStub struct {
	AnalyticsService
	value string
	calls int
}

func (s *monitorFilterStub) CurrentBetsFiltered(_ context.Context, user, game string, limit int, value string) ([]operations.MonitorBet, error) {
	s.value = value
	s.calls++
	return []operations.MonitorBet{}, nil
}
func TestMonitorPlayerTypeFilter(t *testing.T) {
	for _, v := range []string{"", "all", "real", "virtual", "bad"} {
		t.Run(v, func(t *testing.T) {
			stub := &monitorFilterStub{}
			s := &Server{analytics: stub}
			w := httptest.NewRecorder()
			s.adminCurrentBets(w, httptest.NewRequest("GET", "/v1/admin/monitor/bets?player_type="+v, nil))
			if v == "bad" {
				if w.Code != 400 || stub.calls != 0 {
					t.Fatalf("status=%d calls=%d", w.Code, stub.calls)
				}
				return
			}
			if w.Code != 200 || stub.calls != 1 || stub.value != v {
				t.Fatalf("status=%d filter=%s calls=%d", w.Code, stub.value, stub.calls)
			}
		})
	}
}
