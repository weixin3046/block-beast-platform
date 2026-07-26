package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/leaderboard"
	"github.com/block-beast/platform/internal/config"
)

type stubLeaderboardService struct {
	err error
}

func (stub stubLeaderboardService) ListDaily(context.Context, time.Time, string, string, int) ([]leaderboard.Entry, error) {
	return []leaderboard.Entry{}, stub.err
}

func TestDailyLeaderboardValidatesDateAndFilters(t *testing.T) {
	newServer := func(stub stubLeaderboardService) *Server {
		return New(
			config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil, WithLeaderboards(stub),
		)
	}
	for _, testCase := range []struct {
		url  string
		stub stubLeaderboardService
		want int
	}{
		{url: "/v1/leaderboards/daily?date=invalid", want: http.StatusBadRequest},
		{url: "/v1/leaderboards/daily?currency=BTC", stub: stubLeaderboardService{err: leaderboard.ErrInvalidCurrency}, want: http.StatusBadRequest},
		{url: "/v1/leaderboards/daily?currency=USDT", want: http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodGet, testCase.url, nil)
		response := httptest.NewRecorder()
		newServer(testCase.stub).Handler().ServeHTTP(response, request)
		if response.Code != testCase.want {
			t.Fatalf("%s status = %d, want %d", testCase.url, response.Code, testCase.want)
		}
	}
}
