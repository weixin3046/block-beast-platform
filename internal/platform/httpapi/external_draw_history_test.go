package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/platform/lotterydraw"
)

func TestExternalDrawHistoryReturnsRecordsForAuthenticatedPlayer(t *testing.T) {
	reader := &externalDrawHistoryStub{items: []lotterydraw.Record{{Issue: "6653", Room: []int{4}}}}
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)), WithExternalDrawHistory(reader))
	request := httptest.NewRequest(http.MethodGet, "/v1/external-draws/star_sea/history?count=2", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{"player"}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if reader.game != "star_sea" || reader.count != 2 {
		t.Fatalf("reader request = game %q count %d", reader.game, reader.count)
	}
	if !strings.Contains(response.Body.String(), `"game":"star_sea"`) || !strings.Contains(response.Body.String(), `"issue":"6653"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestExternalDrawHistoryRejectsUnauthenticatedRequest(t *testing.T) {
	server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)), WithExternalDrawHistory(&externalDrawHistoryStub{}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/external-draws/star_sea/history", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExternalDrawHistoryMapsUnavailableInvalidAndProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		reader ExternalDrawHistoryReader
		path   string
		want   int
	}{
		{"unavailable", nil, "/v1/external-draws/star_sea/history", http.StatusServiceUnavailable},
		{"unknown game", &externalDrawHistoryStub{}, "/v1/external-draws/unknown/history", http.StatusBadRequest},
		{"invalid count", &externalDrawHistoryStub{}, "/v1/external-draws/star_sea/history?count=101", http.StatusBadRequest},
		{"provider failure", &externalDrawHistoryStub{err: errors.New("provider failed")}, "/v1/external-draws/star_sea/history", http.StatusBadGateway},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options := []Option{WithAuth(NewAuthenticator(testSecret))}
			if testCase.reader != nil {
				options = append(options, WithExternalDrawHistory(testCase.reader))
			}
			server := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, options...)
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{"player"}))
			response := httptest.NewRecorder()

			server.Handler().ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, testCase.want, response.Body.String())
			}
		})
	}
}

type externalDrawHistoryStub struct {
	items []lotterydraw.Record
	err   error
	game  string
	count int
}

func (stub *externalDrawHistoryStub) History(_ context.Context, game string, count int) ([]lotterydraw.Record, error) {
	stub.game, stub.count = game, count
	return stub.items, stub.err
}
