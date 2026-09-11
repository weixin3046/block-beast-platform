package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestAdminCommissionInvalidTimes(t *testing.T) {
	for _, query := range []string{"from=bad", "to=bad", "from=2026-09-02T00:00:00Z&to=2026-09-01T00:00:00Z", "from=2026-09-01T00:00:00Z&to=2026-09-01T00:00:00Z"} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			(&Server{}).adminCommissions(w, httptest.NewRequest("GET", "/v1/admin/commissions?"+query, nil))
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
