package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/block-beast/platform/internal/application/audit"
	"github.com/block-beast/platform/internal/application/leaderboard"
)

type LeaderboardService interface {
	List(context.Context, string, string, int) (leaderboard.Board, error)
	GetRules(context.Context, string, string) (leaderboard.RewardRuleSet, error)
	ReplaceRules(context.Context, leaderboard.RewardRuleSet) (leaderboard.RewardRuleSet, error)
	ListDistributions(context.Context, leaderboard.DistributionQuery) ([]leaderboard.RewardDistribution, error)
}

func WithLeaderboards(service LeaderboardService) Option {
	return func(server *Server) { server.leaderboards = service }
}
func (server *Server) leaderboard(writer http.ResponseWriter, request *http.Request) {
	if server.leaderboards == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "leaderboards are unavailable"})
		return
	}
	claims, _ := ClaimsFromContext(request.Context())
	var item leaderboard.Board
	var err error
	if personalized, ok := server.leaderboards.(interface {
		ListForUser(context.Context, string, string, int, string) (leaderboard.Board, error)
	}); ok {
		item, err = personalized.ListForUser(request.Context(), request.URL.Query().Get("period"), request.URL.Query().Get("currency"), queryLimit(request, 50), claims.Subject)
	} else {
		item, err = server.leaderboards.List(request.Context(), request.URL.Query().Get("period"), request.URL.Query().Get("currency"), queryLimit(request, 50))
	}
	if errors.Is(err, leaderboard.ErrInvalidPeriod) || errors.Is(err, leaderboard.ErrInvalidCurrency) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list leaderboard"})
		return
	}
	var viewerID int64
	if !isStaff(claims) && server.userAdmin != nil {
		if u, e := server.userAdmin.CurrentUser(request.Context(), claims.Subject); e == nil {
			viewerID = u.ID
		}
	}
	for i := range item.Items {
		if !isStaff(claims) && item.Items[i].UserID != viewerID {
			item.Items[i].Available = nil
			item.Items[i].AvailableMinor = nil
		}
	}
	server.writePublicJSON(writer, request, http.StatusOK, item)
}
func (server *Server) leaderboardRules(writer http.ResponseWriter, request *http.Request) {
	if server.leaderboards == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "leaderboards are unavailable"})
		return
	}
	item, err := server.leaderboards.GetRules(request.Context(), request.URL.Query().Get("period_type"), request.URL.Query().Get("currency"))
	if errors.Is(err, leaderboard.ErrInvalidRewardRules) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list leaderboard reward rules"})
		return
	}
	writeJSON(writer, http.StatusOK, item)
}
func (server *Server) replaceLeaderboardRules(writer http.ResponseWriter, request *http.Request) {
	var input leaderboard.RewardRuleSet
	if err := decodeStrictJSON(writer, request, &input); err != nil {
		return
	}
	item, err := server.leaderboards.ReplaceRules(request.Context(), input)
	switch {
	case errors.Is(err, leaderboard.ErrInvalidRewardRules):
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, leaderboard.ErrRewardRuleConflict):
		writeJSON(writer, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to replace leaderboard reward rules"})
	default:
		claims, _ := ClaimsFromContext(request.Context())
		server.recordAudit(request.Context(), audit.Entry{ActorUserID: claims.Subject, Action: "leaderboard_reward_rules.replace", TargetType: "leaderboard_reward_rules", TargetID: item.PeriodType + ":" + item.Currency, Payload: map[string]any{"version": item.Version}})
		writeJSON(writer, http.StatusOK, item)
	}
}
func (server *Server) leaderboardRewards(writer http.ResponseWriter, request *http.Request) {
	items, err := server.leaderboards.ListDistributions(request.Context(), leaderboard.DistributionQuery{PeriodType: request.URL.Query().Get("period_type"), Currency: request.URL.Query().Get("currency"), User: request.URL.Query().Get("user"), Limit: queryLimit(request, 50), Offset: queryOffset(request)})
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list leaderboard rewards"})
		return
	}
	writeJSON(writer, http.StatusOK, items)
}
