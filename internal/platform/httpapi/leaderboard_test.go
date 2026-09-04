package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/block-beast/platform/internal/application/leaderboard"
	"github.com/block-beast/platform/internal/config"
)

type stubLeaderboardService struct {
	err error
}

func (stub stubLeaderboardService) List(context.Context, string, string, int) (leaderboard.Board, error) {
	return leaderboard.Board{}, stub.err
}
func (stub stubLeaderboardService) GetRules(context.Context, string, string) (leaderboard.RewardRuleSet, error) {
	return leaderboard.RewardRuleSet{}, stub.err
}
func (stub stubLeaderboardService) ReplaceRules(context.Context, leaderboard.RewardRuleSet) (leaderboard.RewardRuleSet, error) {
	return leaderboard.RewardRuleSet{}, stub.err
}
func (stub stubLeaderboardService) ListDistributions(context.Context, leaderboard.DistributionQuery) ([]leaderboard.RewardDistribution, error) {
	return nil, stub.err
}

func TestLeaderboardValidatesPeriodAndCurrency(t *testing.T) {
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
		{url: "/v1/leaderboards?period=bad&currency=USDT", stub: stubLeaderboardService{err: leaderboard.ErrInvalidPeriod}, want: http.StatusBadRequest},
		{url: "/v1/leaderboards?period=today", stub: stubLeaderboardService{err: leaderboard.ErrInvalidCurrency}, want: http.StatusBadRequest},
		{url: "/v1/leaderboards?period=today&currency=USDT", want: http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodGet, testCase.url, nil)
		response := httptest.NewRecorder()
		newServer(testCase.stub).Handler().ServeHTTP(response, request)
		if response.Code != testCase.want {
			t.Fatalf("%s status = %d, want %d", testCase.url, response.Code, testCase.want)
		}
	}
}
