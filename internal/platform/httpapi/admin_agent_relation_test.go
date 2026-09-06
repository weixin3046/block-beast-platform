package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	agentapp "github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/config"
)

type adminRelationStub struct {
	AgentService
	parent string
	err    error
	target string
}

func (s *adminRelationStub) GetRelation(_ context.Context, id string) (agentapp.Relation, error) {
	s.target = id
	return agentapp.Relation{UserID: id, ParentUserID: s.parent}, s.err
}

type relationResolver struct{ missing bool }

func (r relationResolver) InternalUserIDByPublicID(context.Context, int64) (string, error) {
	if r.missing {
		return "", errors.New("missing")
	}
	return "internal-player", nil
}
func (r relationResolver) PublicUserID(context.Context, string) (int64, error) { return 100006, nil }

func TestAdminAgentRelation(t *testing.T) {
	for _, tc := range []struct {
		name, role, id, parent string
		missing, fail          bool
		status                 int
		body                   string
	}{
		{name: "anonymous", id: "100009", status: 401},
		{name: "player forbidden", role: "player", id: "100009", status: 403},
		{name: "admin", role: "admin", id: "100009", parent: "internal-parent", status: 200, body: `"parent_user_id":100006`},
		{name: "operator no parent", role: "operator", id: "100009", status: 200, body: `"parent_user_id":null`},
		{name: "invalid", role: "admin", id: "bad", status: 400},
		{name: "invite code", role: "admin", id: "10001", status: 400},
		{name: "missing", role: "admin", id: "100009", missing: true, status: 404},
		{name: "service failure", role: "admin", id: "100009", fail: true, status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &adminRelationStub{parent: tc.parent}
			if tc.fail {
				a.err = errors.New("database failure")
			}
			s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithAgents(a), WithPublicUserResolver(relationResolver{tc.missing}))
			r := httptest.NewRequest("GET", "/v1/admin/users/"+tc.id+"/agent-relation", nil)
			if tc.role != "" {
				r.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-subject", []string{tc.role}))
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && (a.target != "internal-player" || !strings.Contains(w.Body.String(), `"user_id":100009`)) {
				t.Fatal("must query requested player, not authenticated admin")
			}
			if tc.status < 500 && tc.status != 200 && a.target != "" {
				t.Fatal("rejected request reached service")
			}
			if strings.Contains(w.Body.String(), "internal-") {
				t.Fatal("internal ID leaked")
			}
		})
	}
}
