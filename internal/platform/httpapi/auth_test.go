package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func issueTestToken(t *testing.T, subject string, roles []string) string {
	t.Helper()
	token, err := identity.IssueAccessToken([]byte(testSecret), subject, roles, time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func okHandler(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusNoContent)
}

func TestAuthenticateRejectsMissingOrInvalidTokens(t *testing.T) {
	authenticator := NewAuthenticator(testSecret)
	handler := authenticator.Authenticate(okHandler)

	request := httptest.NewRequest(http.MethodGet, "/v1/rounds", nil)
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/rounds", nil)
	request.Header.Set("Authorization", "Bearer not-a-token")
	response = httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", response.Code)
	}

	otherAuthenticator := NewAuthenticator("ffffffffffffffffffffffffffffffff")
	request = httptest.NewRequest(http.MethodGet, "/v1/rounds", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", nil))
	response = httptest.NewRecorder()
	otherAuthenticator.Authenticate(okHandler)(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d, want 401", response.Code)
	}
}

func TestAuthenticateInjectsClaimsIntoContext(t *testing.T) {
	authenticator := NewAuthenticator(testSecret)
	var claims identity.AccessTokenClaims
	var found bool
	handler := authenticator.Authenticate(func(writer http.ResponseWriter, request *http.Request) {
		claims, found = ClaimsFromContext(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/rounds", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if !found || claims.Subject != "user-1" || !claims.HasRole(identity.RolePlayer) {
		t.Fatalf("claims = %+v, found = %v", claims, found)
	}
}

func TestRequireRolesEnforcesMembership(t *testing.T) {
	authenticator := NewAuthenticator(testSecret)
	handler := authenticator.RequireRoles(okHandler, identity.RoleAdmin, identity.RoleOperator)

	request := httptest.NewRequest(http.MethodPost, "/v1/rounds/r1/cancel", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{identity.RolePlayer}))
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("player role status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/rounds/r1/cancel", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-1", []string{identity.RoleAdmin}))
	response = httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("admin role status = %d, want 204", response.Code)
	}
}

func TestAuthorizeAccount(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if !authorizeAccount(request, "anyone") {
		t.Fatal("without claims (auth disabled) access must be allowed")
	}
	withClaims := func(subject string, roles []string) *http.Request {
		claims := identity.AccessTokenClaims{Subject: subject, Roles: roles}
		return httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(context.Background(), claimsContextKey{}, claims))
	}
	if !authorizeAccount(withClaims("user-1", []string{identity.RolePlayer}), "user-1") {
		t.Fatal("owner must be allowed")
	}
	if authorizeAccount(withClaims("user-1", []string{identity.RolePlayer}), "user-2") {
		t.Fatal("other account must be forbidden for player")
	}
	if !authorizeAccount(withClaims("ops-1", []string{identity.RoleOperator}), "user-2") {
		t.Fatal("operator must be allowed on any account")
	}
}

func TestLoginEndpointValidation(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"login_name":"a","password":"b"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing login service status = %d, want 503", response.Code)
	}
}
