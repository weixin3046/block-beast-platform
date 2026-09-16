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

	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/config"
)

func TestLuluMenusReturnsAuthenticatedConfiguration(t *testing.T) {
	menus := &luluMenusStub{value: operations.LuluMenus{ServerTime: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), Games: []operations.LuluMenuGame{{
		Code: "lulu-xdy", Name: "星海逃杀", Rooms: []operations.LuluMenuRoom{{ID: "room-194", Code: "hash_rate_1940", Name: "1.94", Plays: []operations.LuluMenuPlay{{Code: "direct", Name: "直选", Outcomes: []byte(`["1","2"]`), CurrencyConfigs: []operations.LuluPlayCurrencyConfig{{Currency: "POINTS", PayoutMultiplier: 750, PayoutDivisor: 100, MinStakeMinor: 1, MaxStakeMinor: 2000}}}}}},
	}}}}
	server := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil,
		WithAuth(NewAuthenticator(testSecret)), WithLuluMenus(menus))
	request := httptest.NewRequest(http.MethodGet, "/v1/lulu/menus", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{"player"}))
	response := httptest.NewRecorder()

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"code":"lulu-xdy"`, `"name":"星海逃杀"`, `"id":"room-194"`, `"payout_multiplier":750`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("body=%s, missing %s", response.Body.String(), want)
		}
	}
}

func TestLuluMenusRequiresAuthenticationAndService(t *testing.T) {
	server := newAmountTestServer(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil, WithAuth(NewAuthenticator(testSecret)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/lulu/menus", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/lulu/menus", nil)
	request.Header.Set("Authorization", "Bearer "+issueTestToken(t, "player-1", []string{"player"}))
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured status=%d", response.Code)
	}
}

type luluMenusStub struct {
	value operations.LuluMenus
	err   error
}

func (s *luluMenusStub) GetLuluMenus(context.Context) (operations.LuluMenus, error) {
	return s.value, s.err
}
func (s *luluMenusStub) UpdateLuluRoomPlayConfig(context.Context, operations.LuluRoomPlayConfigUpdate) (operations.LuluMenus, error) {
	return s.value, s.err
}
func (s *luluMenusStub) UpdateLuluRoomPlayConfigs(context.Context, []operations.LuluRoomPlayConfigUpdate) (operations.LuluMenus, error) {
	return s.value, s.err
}
func (s *luluMenusStub) UpdateLuluPrimeTimeConfig(context.Context, operations.LuluPrimeTimeConfigUpdate) ([]operations.LuluPrimeTimeConfigUpdate, error) {
	return nil, s.err
}
func (s *luluMenusStub) UpdateLuluPrimeTimeConfigs(context.Context, []operations.LuluPrimeTimeConfigUpdate) ([]operations.LuluPrimeTimeConfigUpdate, error) {
	return nil, s.err
}
func (s *luluMenusStub) GetLuluPrimeTimeConfigs(context.Context) ([]operations.LuluPrimeTimeConfigUpdate, error) {
	return nil, s.err
}
