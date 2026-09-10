package lulu

import (
	"context"
	"errors"
	app "github.com/block-beast/platform/internal/application/lulu"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBalanceQuery(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		fallback         bool
		bad              bool
	}{
		{"precision", `{"code":0,"data":{"item_id":102201,"item_num":5160.07704}}`, "5160.07704", false, false},
		{"zero", `{"code":0,"data":{"item_id":102201,"item_num":0}}`, "0", false, false},
		{"negative", `{"code":0,"data":{"item_id":102201,"item_num":-1}}`, "", false, true},
		{"missing amount", `{"code":0,"data":{"item_id":102201}}`, "", false, true},
		{"fallback", `{"code":0,"data":null}`, "2.123456", true, false},
		{"business error", `{"code":7}`, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t)
			calls := 0
			c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.Header.Get("token") == "" || r.Header.Get("User-Agent") != "BestHTTP" {
					t.Fatal("invalid request")
				}
				body := tc.body
				if calls == 1 && r.URL.RequestURI() != "/player/item?item_id=102201" {
					t.Fatal(r.URL)
				}
				if calls == 2 {
					if !tc.fallback || r.URL.Path != "/player/items" {
						t.Fatal("unexpected fallback")
					}
					body = `{"code":0,"data":[{"item_id":102201,"item_num":2.123456}]}`
				}
				encrypted, err := c.encrypt([]byte(body))
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(encrypted)))}, nil
			})
			got, err := c.Balance(context.Background())
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("%s %v", got, err)
			}
			wantCalls := 1
			if tc.fallback {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatal(calls)
			}
		})
	}
	c := fixture(t)
	calls := 0
	c.http.Transport = transport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := c.Balance(context.Background()); !errors.Is(err, app.ErrTokenInvalid) || calls != 1 {
		t.Fatal(err, calls)
	}
}
