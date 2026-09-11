package httpapi

import (
	"context"
	agentapp "github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/domain/identity"
	"net/http/httptest"
	"strings"
	"testing"
)

type agentLevelStub struct {
	AgentService
	owner  string
	target int64
	level  int
	err    error
}

func (s *agentLevelStub) SetDirectPlayerLevel(_ context.Context, owner string, target int64, level int) error {
	s.owner = owner
	s.target = target
	s.level = level
	return s.err
}
func TestSelfLevelHTTP(t *testing.T) {
	for _, tc := range []struct {
		body   string
		login  bool
		err    error
		status int
	}{
		{`{"agent_level":2}`, false, nil, 401}, {`{}`, true, nil, 400}, {`{"agent_level":null}`, true, nil, 400}, {`{"agent_level":2}`, true, nil, 200}, {`{"agent_level":0}`, true, nil, 200}, {`{"agent_level":3}`, true, agentapp.ErrChildLevelForbidden, 403},
	} {
		t.Run(tc.body, func(t *testing.T) {
			stub := &agentLevelStub{err: tc.err}
			s := &Server{agents: stub}
			r := httptest.NewRequest("PUT", "/v1/agents/me/direct-players/10052/agent-level?user_id=other", strings.NewReader(tc.body))
			r.SetPathValue("userID", "10052")
			if tc.login {
				r = r.WithContext(context.WithValue(r.Context(), claimsContextKey{}, identity.AccessTokenClaims{Subject: "caller"}))
			}
			w := httptest.NewRecorder()
			s.setDirectPlayerLevel(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 200 && (stub.owner != "caller" || stub.target != 10052) {
				t.Fatalf("wrong identity %+v", stub)
			}
		})
	}
}
