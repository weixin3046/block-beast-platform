package lotterydraw

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoryDecryptsStarSeaRecords(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/lottery/history" || request.URL.Query().Get("count") != "2" {
			t.Fatalf("request path/query = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "message": "success", "data": xorFixture(`[{"issue":"6653","room":[4]}]`)})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.History(context.Background(), GameStarSea, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Issue != "6653" || len(items[0].Room) != 1 || items[0].Room[0] != 4 {
		t.Fatalf("items = %#v", items)
	}
}

func TestHistoryUsesAngryFeatherEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/crow/history" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": xorFixture(`[{"issue":"18","room":[2]}]`)})
	}))
	defer server.Close()

	client, _ := New(server.URL)
	items, err := client.History(context.Background(), GameAngryFeather, 1)
	if err != nil || len(items) != 1 || items[0].Room[0] != 2 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestHistoryRejectsInvalidProviderData(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"business error", map[string]any{"code": 1, "data": xorFixture(`[]`)}},
		{"invalid ciphertext", map[string]any{"code": 0, "data": "not encrypted"}},
		{"out of range room", map[string]any{"code": 0, "data": xorFixture(`[{"issue":"1","room":[9]}]`)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_ = json.NewEncoder(writer).Encode(testCase.body)
			}))
			defer server.Close()
			client, _ := New(server.URL)
			if _, err := client.History(context.Background(), GameStarSea, 1); err == nil {
				t.Fatal("History error = nil")
			}
		})
	}
}

func TestHistoryRejectsUnknownGameAndUpstreamFailure(t *testing.T) {
	client, err := New("http://provider.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.History(context.Background(), "unknown", 1); err == nil {
		t.Fatal("unknown game was accepted")
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	client, _ = New(server.URL)
	if _, err := client.History(context.Background(), GameStarSea, 1); err == nil {
		t.Fatal("upstream HTTP failure was accepted")
	}
}

func xorFixture(plain string) string {
	const key = "272fe2cc435c5a73a4a1ac24e91c9213"
	output := make([]rune, 0, len(plain))
	for i, value := range []rune(plain) {
		output = append(output, value^rune(key[i%len(key)]))
	}
	return string(output)
}
