package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/auth"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
)

type stubUserAdmin struct {
	roleError error
}

type stubSecondaryPasswords struct {
	setErr      error
	changeErr   error
	setCalls    int
	changeCalls int
}

func (stub *stubSecondaryPasswords) SetSecondaryPassword(context.Context, string, string, string) error {
	stub.setCalls++
	return stub.setErr
}

func (*stubSecondaryPasswords) VerifySecondaryPassword(context.Context, string, string) error {
	return nil
}

func (stub *stubSecondaryPasswords) ChangeSecondaryPassword(context.Context, string, string, string) error {
	stub.changeCalls++
	return stub.changeErr
}

func (stubUserAdmin) ListUsers(context.Context, string, string, int) ([]operations.User, error) {
	return nil, nil
}

func (stubUserAdmin) SetUserStatus(context.Context, string, string, string) error {
	return nil
}

func (stubUserAdmin) CurrentUser(context.Context, string) (operations.User, error) {
	return operations.User{ID: 100000, LoginName: "player", InvitationCode: 10001}, nil
}

func (stubUserAdmin) UpdateCurrentProfile(context.Context, string, string, string) (operations.User, error) {
	return operations.User{ID: 100000}, nil
}

func (stubUserAdmin) SetAgentLevel(context.Context, string, int) error { return nil }

func (stubUserAdmin) ListRoles(context.Context) ([]operations.Role, error) {
	return []operations.Role{{Code: identity.RoleAdmin}}, nil
}

func (stub stubUserAdmin) SetUserRoles(context.Context, string, string, []string) (operations.RoleAssignment, error) {
	return operations.RoleAssignment{UserID: 100000, Roles: []string{identity.RoleOperator}}, stub.roleError
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

func TestSecondaryPasswordFirstSetupToleratesCurrentPasswordField(t *testing.T) {
	passwords := &stubSecondaryPasswords{changeErr: auth.ErrSecondaryPasswordNotSet}
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)), WithSecondaryPasswords(passwords),
	)
	request := httptest.NewRequest(http.MethodPut, "/v1/users/me/secondary-password", strings.NewReader(
		`{"current_secondary_password":"stale-value","secondary_password":"new-password"}`,
	))
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if passwords.changeCalls != 1 || passwords.setCalls != 1 {
		t.Fatalf("change calls = %d, set calls = %d", passwords.changeCalls, passwords.setCalls)
	}
}

func TestSecondaryPasswordChangeStillRejectsWrongCurrentPassword(t *testing.T) {
	passwords := &stubSecondaryPasswords{changeErr: auth.ErrInvalidCredentials}
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)), WithSecondaryPasswords(passwords),
	)
	request := httptest.NewRequest(http.MethodPut, "/v1/users/me/secondary-password", strings.NewReader(
		`{"current_secondary_password":"wrong-password","secondary_password":"new-password"}`,
	))
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if passwords.changeCalls != 1 || passwords.setCalls != 0 {
		t.Fatalf("change calls = %d, set calls = %d", passwords.changeCalls, passwords.setCalls)
	}
}

func TestSetAgentLevelRequiresExplicitValue(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"agent_level":0}`, http.StatusOK},
		{`{}`, http.StatusBadRequest},
		{`{"agent_level":null}`, http.StatusBadRequest},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)), WithUserAdmin(stubUserAdmin{}))
			request := httptest.NewRequest(http.MethodPut, "/v1/admin/users/100000/agent-level", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-1", []string{identity.RoleAdmin}))
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
		})
	}
}
