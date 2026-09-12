package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/block-beast/platform/internal/platform/lotterydraw"
)

type ExternalDrawHistoryReader interface {
	History(ctx context.Context, game string, count int) ([]lotterydraw.Record, error)
}

func WithExternalDrawHistory(reader ExternalDrawHistoryReader) Option {
	return func(server *Server) { server.externalDrawHistory = reader }
}

func (server *Server) externalDrawHistoryEndpoint(writer http.ResponseWriter, request *http.Request) {
	if server.externalDrawHistory == nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "external draw history is unavailable"})
		return
	}
	game := request.PathValue("game")
	if game != lotterydraw.GameStarSea && game != lotterydraw.GameAngryFeather {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unknown external draw game"})
		return
	}
	count := 100
	if value := request.URL.Query().Get("count"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "count must be between 1 and 100"})
			return
		}
		count = parsed
	}
	items, err := server.externalDrawHistory.History(request.Context(), game, count)
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]string{"error": "external draw history is unavailable"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"game": game, "items": items})
}
