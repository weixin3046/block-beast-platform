package lulutrend

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (fn roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestHistoryMapsOfficialTrendRecord(t *testing.T) {
	client, err := NewClient("http://159.75.13.86:9082/api/trend", &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/trend" || request.URL.Query().Get("game") != "xdy" || request.URL.Query().Get("limit") != "60" {
			t.Fatalf("request=%s", request.URL.String())
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"game":"xdy","list":[{"i":42,"t":"2026-09-13T13:22:21.267Z","k":[1]}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	records, err := client.History(context.Background(), "xdy")
	if err != nil || len(records) != 1 || records[0].Round != "42" || records[0].Result[0] != "1" {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}
