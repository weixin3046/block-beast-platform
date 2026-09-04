package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/platform/usermessage"
)

func TestChineseErrorResponsePreservesContract(t *testing.T) {
	source := map[string]any{"error": "too many login attempts; try again later", "status": "login_throttled", "retry_after_seconds": 30}
	w := httptest.NewRecorder()
	writeJSON(w, 429, source)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 429 || out["error"] != "登录尝试过于频繁，请稍后重试" || out["status"] != "login_throttled" || out["retry_after_seconds"] != float64(30) {
		t.Fatalf("unexpected response %d %v", w.Code, out)
	}
	if source["error"] != "too many login attempts; try again later" {
		t.Fatal("mutated source error")
	}
	w = httptest.NewRecorder()
	writeJSON(w, 500, map[string]string{"error": "database credentials: secret"})
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["error"] != usermessage.Fallback {
		t.Fatal("unknown error was exposed")
	}
	// User-authored text and success payloads are not translated.
	w = httptest.NewRecorder()
	writeJSON(w, 200, map[string]string{"body": "hello", "status": "won", "error": "user text"})
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["body"] != "hello" || out["status"] != "won" || out["error"] != "user text" {
		t.Fatal("success data was modified")
	}
}

func TestChineseRoutingErrors(t *testing.T) {
	s := New(config.Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, readinessChecker{}, nil, nil, nil, nil)
	for _, tt := range []struct {
		method, path string
		status       int
		message      string
	}{
		{"GET", "/v1/does-not-exist", 404, "请求的接口不存在"},
		{"POST", "/healthz", 405, "该接口不支持此请求方法"},
		{"GET", "/healthz", 200, ""},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
		var out map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != tt.status || out["error"] != tt.message {
			t.Fatalf("%s %s: %d %v", tt.method, tt.path, w.Code, out)
		}
		if tt.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("Allow = %q", w.Header().Get("Allow"))
		}
	}
}

func TestChineseRoutingPreservesRedirectsAndCustomMethods(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("CUSTOM /example", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, path := range []string{"/not//found", "/example", "/a/../example"} {
		plain, localized := httptest.NewRecorder(), httptest.NewRecorder()
		mux.ServeHTTP(plain, httptest.NewRequest("GET", path, nil))
		chineseRoutingErrors(mux).ServeHTTP(localized, httptest.NewRequest("GET", path, nil))
		if plain.Code != localized.Code || plain.Header().Get("Allow") != localized.Header().Get("Allow") || plain.Header().Get("Location") != localized.Header().Get("Location") {
			t.Fatalf("routing behavior changed for %s", path)
		}
	}
}
