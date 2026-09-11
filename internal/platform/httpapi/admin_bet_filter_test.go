package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/block-beast/platform/internal/application/operations"
)

type betFilterStub struct {
	AnalyticsService
	query operations.BetQuery
	calls int
}

func (s *betFilterStub) ListAdminBets(_ context.Context, q operations.BetQuery) ([]operations.AdminBet, error) {
	s.query = q
	s.calls++
	return []operations.AdminBet{}, nil
}
func TestAdminBetPlayerFilter(t *testing.T) {
	for _, value := range []string{"", "all", "real", "virtual", "invalid"} {
		t.Run(value, func(t *testing.T) {
			stub := &betFilterStub{}
			server := &Server{analytics: stub}
			response := httptest.NewRecorder()
			server.adminBets(response, httptest.NewRequest("GET", "/v1/admin/bets?player_type="+value, nil))
			if value == "invalid" {
				if response.Code != 400 || stub.calls != 0 {
					t.Fatalf("invalid filter: %d calls=%d", response.Code, stub.calls)
				}
			} else if response.Code != 200 || stub.calls != 1 || stub.query.PlayerType != value {
				t.Fatalf("filter %q: status=%d query=%+v", value, response.Code, stub.query)
			}
		})
	}
}
