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

type stubOperations struct {
	configError error
}

func (stubOperations) ListAnnouncements(context.Context, bool, int) ([]operations.Announcement, error) {
	return nil, nil
}
func (stubOperations) CreateAnnouncement(context.Context, operations.AnnouncementInput) (operations.Announcement, error) {
	return operations.Announcement{}, nil
}
func (stubOperations) UpdateAnnouncement(context.Context, string, operations.AnnouncementInput) (operations.Announcement, error) {
	return operations.Announcement{}, nil
}
func (stubOperations) ListAuditLogs(context.Context, string, string, int) ([]operations.AuditLog, error) {
	return nil, nil
}
func (stub stubOperations) PublicConfig(context.Context, string) (operations.PlatformConfig, error) {
	return operations.PlatformConfig{Key: "lobby.banner"}, stub.configError
}
func (stubOperations) ListConfigs(context.Context, string, int) ([]operations.PlatformConfig, error) {
	return nil, nil
}
func (stub stubOperations) PutConfig(context.Context, string, string, operations.ConfigInput) (operations.PlatformConfig, error) {
	return operations.PlatformConfig{Key: "lobby.banner"}, stub.configError
}

func TestPublicConfigHidesMissingOrInternalConfig(t *testing.T) {
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil,
		WithOperations(stubOperations{configError: operations.ErrConfigNotFound}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/configs/internal.secret", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestPutConfigMapsVersionConflict(t *testing.T) {
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)),
		WithOperations(stubOperations{configError: operations.ErrConfigVersionConflict}),
	)
	request := httptest.NewRequest(
		http.MethodPut, "/v1/admin/configs/lobby.banner",
		strings.NewReader(`{"value":{"enabled":true},"visibility":"public","expected_version":1}`),
	)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "admin-1", []string{identity.RoleAdmin}))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d", response.Code)
	}
}
