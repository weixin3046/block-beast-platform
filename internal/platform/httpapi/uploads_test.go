package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/block-beast/platform/internal/platform/objectstorage"
)

type stubUploadService struct {
	authorization uploads.Authorization
	upload        uploads.Upload
	err           error
}

func (stub stubUploadService) Authorize(context.Context, string, string, int64) (uploads.Authorization, error) {
	return stub.authorization, stub.err
}

func (stub stubUploadService) Confirm(context.Context, string, string) (uploads.Upload, error) {
	return stub.upload, stub.err
}

func (stub stubUploadService) Find(context.Context, string, string) (uploads.Upload, error) {
	return stub.upload, stub.err
}

func (stub stubUploadService) PutContent(context.Context, string, string, string, io.Reader) (uploads.Upload, error) {
	return stub.upload, stub.err
}

func (stub stubUploadService) OpenContent(context.Context, string, string) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error) {
	return nil, objectstorage.ObjectInfo{}, stub.err
}

func (stub stubUploadService) OpenPublicAvatar(context.Context, int64) (objectstorage.ReadSeekCloser, objectstorage.ObjectInfo, error) {
	return nil, objectstorage.ObjectInfo{}, stub.err
}

func TestUploadAuthorizationRequiresIdentityAndMapsValidation(t *testing.T) {
	newServer := func(stub stubUploadService) *Server {
		return New(
			config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil,
			WithAuth(NewAuthenticator(testSecret)), WithUploads(stub),
		)
	}
	body := `{"content_type":"image/png","size_bytes":123}`
	request := httptest.NewRequest(http.MethodPost, "/v1/uploads/authorize", strings.NewReader(body))
	response := httptest.NewRecorder()
	newServer(stubUploadService{}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/uploads/authorize", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
	response = httptest.NewRecorder()
	newServer(stubUploadService{err: uploads.ErrInvalidUpload}).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid upload status = %d", response.Code)
	}
}

func TestUploadConfirmationMapsLifecycleErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{uploads.ErrUploadNotFound, http.StatusNotFound},
		{uploads.ErrUploadExpired, http.StatusConflict},
		{uploads.ErrObjectMismatch, http.StatusConflict},
		{nil, http.StatusOK},
	}
	for _, testCase := range tests {
		server := New(
			config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil,
			WithAuth(NewAuthenticator(testSecret)), WithUploads(stubUploadService{err: testCase.err}),
		)
		request := httptest.NewRequest(http.MethodPost, "/v1/uploads/upload-1/confirm", nil)
		request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != testCase.want {
			t.Fatalf("error %v status = %d, want %d", testCase.err, response.Code, testCase.want)
		}
	}
}

func TestLocalUploadContentMapsLifecycleErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{uploads.ErrUploadNotFound, http.StatusNotFound},
		{uploads.ErrUploadExpired, http.StatusConflict},
		{uploads.ErrObjectMismatch, http.StatusConflict},
		{uploads.ErrContentOperationUnsupported, http.StatusMethodNotAllowed},
		{nil, http.StatusOK},
	}
	for _, testCase := range tests {
		server := New(
			config.Config{UploadMaxBytes: 1024}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
			nil, readinessChecker{}, nil, nil, nil, nil,
			WithAuth(NewAuthenticator(testSecret)),
			WithUploads(stubUploadService{upload: uploads.Upload{ID: "upload-1"}, err: testCase.err}),
		)
		request := httptest.NewRequest(http.MethodPut, "/v1/uploads/upload-1/content", strings.NewReader("content"))
		request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
		request.Header.Set("Content-Type", "image/png")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != testCase.want {
			t.Fatalf("error %v status = %d, want %d", testCase.err, response.Code, testCase.want)
		}
	}
}

func TestLocalUploadDownloadRequiresConfirmedContent(t *testing.T) {
	server := New(
		config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)),
		nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)),
		WithUploads(stubUploadService{err: uploads.ErrUploadNotReady}),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/uploads/upload-1/content", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "user-1", []string{identity.RolePlayer}))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("download pending status = %d, want 409", response.Code)
	}
}
