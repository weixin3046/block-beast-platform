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
	err   error
}

func (s *directPlayerStub) ListDirectPlayers(_ context.Context, owner string, q agentapp.DirectPlayerQuery) (agentapp.DirectPlayers, error) {
	s.owner = owner
	s.query = q
	return agentapp.DirectPlayers{Items: []agentapp.DirectPlayer{}}, s.err
}
func TestDirectPlayersIdentityAndValidation(t *testing.T) {
	for _, tc := range []struct {
		query  string
		login  bool
		status int
	}{
		{"", false, 401}, {"?player_type=bad", true, 400}, {"?from=bad", true, 400},
		{"?parent_user_id=bad", true, 400}, {"?parent_user_id=-1", true, 400},
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

func TestDirectPlayersExpansionAndErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {agentapp.ErrPlayerQueryForbidden, 403}, {agentapp.ErrPlayerQueryInvalid, 400}} {
		stub := &directPlayerStub{err: tc.err}
		s := &Server{agents: stub}
		r := httptest.NewRequest("GET", "/v1/agents/me/direct-players?parent_user_id=10052&cursor=10060&limit=20", nil)
		r = r.WithContext(context.WithValue(r.Context(), claimsContextKey{}, identity.AccessTokenClaims{Subject: "owner"}))
		w := httptest.NewRecorder()
		s.directPlayers(w, r)
		if w.Code != tc.status || stub.owner != "owner" || stub.query.ParentUserID != 10052 || stub.query.Cursor != "10060" || stub.query.Limit != 20 {
			t.Fatalf("status=%d query=%+v", w.Code, stub.query)
		}
	}
}
