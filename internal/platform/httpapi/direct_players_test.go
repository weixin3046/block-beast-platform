package httpapi

import (
	"context"
	agentapp "github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/domain/identity"
	"net/http/httptest"
	"testing"
)

type directPlayerStub struct {
	AgentService
	owner string
	query agentapp.DirectPlayerQuery
}

func (s *directPlayerStub) ListDirectPlayers(_ context.Context, owner string, q agentapp.DirectPlayerQuery) (agentapp.DirectPlayers, error) {
	s.owner = owner
	s.query = q
	return agentapp.DirectPlayers{Items: []agentapp.DirectPlayer{}}, nil
}
func TestDirectPlayersIdentityAndValidation(t *testing.T) {
	for _, tc := range []struct {
		query  string
		login  bool
		status int
	}{
		{"", false, 401}, {"?player_type=bad", true, 400}, {"?from=bad", true, 400},
		{"?from=2026-09-12T00:00:00Z&to=2026-09-11T00:00:00Z", true, 400},
		{"?user_id=other&player_type=real&limit=1&offset=2&from=2026-09-11T00:00:00Z", true, 200},
	} {
		t.Run(tc.query, func(t *testing.T) {
			stub := &directPlayerStub{}
			s := &Server{agents: stub}
			r := httptest.NewRequest("GET", "/v1/agents/me/direct-players"+tc.query, nil)
			if tc.login {
				r = r.WithContext(context.WithValue(r.Context(), claimsContextKey{}, identity.AccessTokenClaims{Subject: "owner"}))
			}
			w := httptest.NewRecorder()
			s.directPlayers(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 200 {
				if stub.owner != "owner" || stub.query.PlayerType != "real" || stub.query.Limit != 1 || stub.query.Offset != 2 || stub.query.From.IsZero() {
					t.Fatalf("wrong scope/query: %+v", stub)
				}
			} else if stub.owner != "" {
				t.Fatal("invalid request reached database")
			}
		})
	}
}
