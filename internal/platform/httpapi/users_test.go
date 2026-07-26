package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
)

type stubUserAdmin struct {
	roleError error
}

func (stubUserAdmin) ListUsers(context.Context, string, string, int) ([]operations.User, error) {
	return nil, nil
}

func (stubUserAdmin) SetUserStatus(context.Context, string, string, string) error {
	return nil
}

func (stubUserAdmin) ListRoles(context.Context) ([]operations.Role, error) {
	return []operations.Role{{Code: identity.RoleAdmin}}, nil
}

func (stub stubUserAdmin) SetUserRoles(context.Context, string, string, []string) (operations.RoleAssignment, error) {
	return operations.RoleAssignment{UserID: "user-1", Roles: []string{identity.RoleOperator}}, stub.roleError
}

func TestRoleManagementRequiresAdminAndMapsSafetyErrors(t *testing.T) {
	newServer := func(stub stubUserAdmin) *Server {
		return New(
			config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil,
			WithAuth(NewAuthenticator(testSecret)), WithUserAdmin(stub),
		)
	}
	body := `{"roles":["operator"]}`
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/users/user-1/roles", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "operator-1", []string{identity.RoleOperator}))
	response := httptest.NewRecorder()
	newServer(stubUserAdmin{}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("operator status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPut, "/v1/admin/users/user-1/roles", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-1", []string{identity.RoleAdmin}))
	response = httptest.NewRecorder()
	newServer(stubUserAdmin{roleError: operations.ErrCannotRemoveLastAdmin}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("last admin status = %d", response.Code)
	}
}
