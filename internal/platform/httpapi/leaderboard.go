package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/block-beast/platform/internal/application/leaderboard"
)

type LeaderboardService interface {
	ListDaily(ctx context.Context, date time.Time, currency, metric string, limit int) ([]leaderboard.Entry, error)
}

func WithLeaderboards(service LeaderboardService) Option {
	return func(server *Server) { server.leaderboards = service }
}

func (server *Server) dailyLeaderboard(writer http.ResponseWriter, request *http.Request) {
	if server.leaderboards == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "leaderboards are unavailable"})
		return
	}
	date := time.Now().UTC()
	if value := request.URL.Query().Get("date"); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "date must use YYYY-MM-DD"})
			return
		}
		date = parsed
	}
	items, err := server.leaderboards.ListDaily(
		request.Context(), date, request.URL.Query().Get("currency"),
		request.URL.Query().Get("metric"), queryLimit(request, 50),
	)
	switch {
	case errors.Is(err, leaderboard.ErrInvalidCurrency), errors.Is(err, leaderboard.ErrInvalidMetric):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list leaderboard"})
	default:
		writeJSON(writer, http.StatusOK, items)
	}
}
