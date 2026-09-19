package luluall

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestHistoryMapsResultsWithoutUsingRankingOrReceptionTime(t *testing.T) {
	for _, tc := range []struct {
		game, remote, record string
		want                 []string
	}{
		{"lh", "cock", `{"roundId":123,"winner":2,"items":[2,1],"drawTimeMs":1789829000000}`, []string{"2"}},
		{"race", "race", `{"roundId":123,"winner":5,"items":[5,3,2,1,6,4]}`, []string{"5"}},
		{"xdy", "steal", `{"roundId":123,"winner":0,"items":[7,1,3],"drawTimeMs":null}`, []string{"1", "3", "7"}},
	} {
		t.Run(tc.game, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/luluall/api/v1/games/"+tc.remote+"/history" || r.URL.Query().Get("limit") != "500" {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				fmt.Fprintf(w, `{"code":0,"data":{"game":%q,"history":[%s]}}`, tc.remote, tc.record)
			}))
			defer server.Close()
			client, err := NewClient(server.URL + "/luluall/api/v1")
			if err != nil {
				t.Fatal(err)
			}
			events, err := client.History(context.Background(), tc.game)
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%v error=%v", events, err)
			}
			e := events[0]
			if e.Game != tc.game || e.Round != "123" || e.CloseAt != nil || !reflect.DeepEqual(e.Result, tc.want) {
				t.Fatalf("event = %+v", e)
			}
		})
	}
}

func TestHistoryRejectsInvalidOrAmbiguousData(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"code":403,"data":{"game":"steal","history":[]}}`,
		`{"code":0,"data":{"game":"race","history":[]}}`,
		`{"code":0,"data":{"game":"steal","history":[{"roundId":0,"items":[1]}]}}`,
		`{"code":0,"data":{"game":"steal","history":[{"roundId":1,"items":[1,1]}]}}`,
		`{"code":0,"data":{"game":"steal","history":[{"roundId":1,"items":[9]}]}}`,
		`{"code":0,"data":{"game":"steal","history":[{"roundId":1,"items":[]}]}}`,
		`{"code":0,"data":{"game":"steal","history":[{"roundId":1,"items":[1]},{"roundId":1,"items":[2]}]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			client, _ := NewClient(server.URL)
			if _, err := client.History(context.Background(), "xdy"); err == nil {
				t.Fatal("accepted invalid history")
			}
		})
	}
}

func TestHTTPFailuresAndRedirectsAreNotAccepted(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusFound} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/secret")
			w.WriteHeader(status)
		}))
		client, _ := NewClient(server.URL)
		if _, err := client.History(context.Background(), "lh"); err == nil {
			t.Fatal("accepted HTTP failure")
		}
		server.Close()
	}
}

func TestRejectsUnsafeBaseURL(t *testing.T) {
	for _, s := range []string{"", "file:///tmp/a", "http://user:password@example.com", "https://example.com?token=secret", "https://example.com/#fragment"} {
		if _, err := NewClient(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
