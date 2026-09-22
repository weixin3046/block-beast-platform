package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/block-beast/platform/internal/application/operations"
)

type betFilterStub struct {
	AnalyticsService
	query operations.BetQuery
	calls int
	items []operations.AdminBet
	total int64
}

func (s *betFilterStub) CountAdminBets(_ context.Context, q operations.BetQuery) (int64, error) {
	return s.total, nil
}

func (s *betFilterStub) ListAdminBets(_ context.Context, q operations.BetQuery) ([]operations.AdminBet, error) {
	s.query = q
	s.calls++
	return s.items, nil
}
func TestAdminBetPagination(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3} {
		size := n
		if size > 2 {
			size = 2
		}
		stub := &betFilterStub{items: make([]operations.AdminBet, size), total: int64(4 + n)}
		s := &Server{analytics: stub}
		w := httptest.NewRecorder()
		s.adminBets(w, httptest.NewRequest("GET", "/v1/admin/bets?limit=2&offset=4", nil))
		var out struct {
			Items         []operations.AdminBet `json:"items"`
			Limit, Offset int
			Total         int64 `json:"total"`
			HasMore       bool  `json:"has_more"`
			Next          *int  `json:"next_offset"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		want := n
		if want > 2 {
			want = 2
		}
		if out.Items == nil || len(out.Items) != want || out.Limit != 2 || out.Offset != 4 || out.HasMore != (n > 2) || out.Total != int64(4+n) {
			t.Fatalf("n=%d body=%s", n, w.Body.String())
		}
		if n > 2 {
			if out.Next == nil || *out.Next != 6 {
				t.Fatal("missing next offset")
			}
		} else if out.Next != nil {
			t.Fatal("unexpected next page")
		}
	}
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
