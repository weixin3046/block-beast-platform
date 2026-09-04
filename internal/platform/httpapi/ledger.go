package httpapi

import (
	"context"
	"errors"
	"github.com/block-beast/platform/internal/application/credit"
	"net/http"
	"strconv"
)

type UnifiedLedgerService interface {
	ListUnifiedLedger(context.Context, string, string, string, int) (credit.LedgerPage, error)
}

func (server *Server) currentUserLedger(w http.ResponseWriter, r *http.Request) {
	service, ok := server.credits.(UnifiedLedgerService)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "ledger service is unavailable"})
		return
	}
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 100 {
			writeJSON(w, 400, map[string]string{"error": "limit must be between 1 and 100"})
			return
		}
		limit = n
	}
	claims, _ := ClaimsFromContext(r.Context())
	out, err := service.ListUnifiedLedger(r.Context(), claims.Subject, r.URL.Query().Get("currency"), r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, credit.ErrInvalidCursor) {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "unable to list ledger"})
		return
	}
	writeJSON(w, 200, out)
}
