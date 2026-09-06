package httpapi

import (
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/application/leaderboard"
	"github.com/block-beast/platform/internal/config"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

type selfBoardStub struct {
	stubLeaderboardService
	t *testing.T
}

func (s selfBoardStub) ListForUser(_ context.Context, period, currency string, limit int, user string) (leaderboard.Board, error) {
	if user != "viewer" || period != "today" || currency != "POINTS" || limit != 1 {
		s.t.Fatalf("identity/filter forwarding %q %q %q %d", user, period, currency, limit)
	}
	balance := "1.234"
	minor := int64(1234)
	return leaderboard.Board{Currency: "POINTS", Items: []leaderboard.Entry{{UserID: 100001, Rank: 1, Available: &balance, AvailableMinor: &minor}}, Self: &leaderboard.Entry{UserID: 100003, Rank: 3, Available: &balance, AvailableMinor: &minor}}, nil
}
func TestLeaderboardSelfCannotImpersonateOrExposeOtherBalance(t *testing.T) {
	s := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithLeaderboards(selfBoardStub{t: t}))
	path := "/v1/leaderboards?period=today&currency=POINTS&limit=1&self_user_id=100001"
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "viewer", []string{"player"}))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Items []map[string]any `json:"items"`
		Self  map[string]any   `json:"self"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Items) != 1 || out.Items[0]["available"] != nil || out.Items[0]["available_minor"] != nil || out.Self["rank"] != float64(3) || out.Self["available"] != "1.234" || out.Self["available_minor"] != nil {
		t.Fatalf("privacy/amount contract %+v", out)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 401 {
		t.Fatalf("anonymous=%d", w.Code)
	}
}
