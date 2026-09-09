package lulu

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	app "github.com/block-beast/platform/internal/application/lulu"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T) *Client {
	t.Helper()
	c, err := NewCredentialClient("https://example.invalid", "987654321", "test-only-token", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func response(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}
}
func TestAuthenticatedEncryption(t *testing.T) {
	c := fixture(t)
	for _, s := range []string{"", "a", "1234567890123456", `{"item_num":123,"toUserId":1234567}`} {
		enc, e := c.encrypt([]byte(s))
		if e != nil {
			t.Fatal(e)
		}
		dec, e := c.decrypt(enc)
		if e != nil || string(dec) != s {
			t.Fatalf("roundtrip %q %v", dec, e)
		}
		raw, _ := base64.StdEncoding.DecodeString(string(enc))
		raw[20] ^= 1
		if _, e = c.decrypt([]byte(base64.StdEncoding.EncodeToString(raw))); e == nil {
			t.Fatal("accepted tampered ciphertext")
		}
	}
	for _, s := range []string{"", "AA==", "not-base64"} {
		if _, e := c.decrypt([]byte(s)); e == nil {
			t.Fatal("accepted malformed ciphertext")
		}
	}
}
func TestTransferNeverRetriesAmbiguousResponse(t *testing.T) {
	c := fixture(t)
	calls := 0
	c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return response(map[string]any{"code": 0, "data": map[string]any{"nickname": "test"}}), nil
		}
		calls++
		body, _ := io.ReadAll(r.Body)
		plain, e := c.decrypt(body)
		if e != nil {
			t.Fatal(e)
		}
		var p map[string]json.Number
		if json.Unmarshal(plain, &p) != nil || p["item_num"] != "12" || p["toUserId"] != "1234567" {
			t.Fatalf("payload %s", plain)
		}
		return nil, errors.New("simulated timeout after send")
	})
	got := c.Transfer(context.Background(), app.Order{ReceiverUID: c.receiver, LuluUID: "1234567", Amount: "12"})
	if got != app.TransferUnknown || calls != 1 {
		t.Fatalf("result %s calls %d", got, calls)
	}
	c.token = ""
	got = c.Transfer(context.Background(), app.Order{ReceiverUID: c.receiver, LuluUID: "1234567", Amount: "12"})
	if got != app.TransferFailed || calls != 1 {
		t.Fatalf("wrong account sent: %s %d", got, calls)
	}
}
func TestPaginationAndRecordIdentity(t *testing.T) {
	c := fixture(t)
	now := time.Now().Truncate(time.Millisecond)
	pages := 0
	c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		pages++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		list := []any{}
		n := 100
		if page == 2 {
			n = 1
		}
		for i := 0; i < n; i++ {
			list = append(list, map[string]any{"user_id": 1234567, "item_id": 102201, "item_num": 1, "time": now.Add(-time.Duration((page-1)*100+i) * time.Second).UnixMilli()})
		}
		return response(map[string]any{"code": 0, "data": map[string]any{"list": list}}), nil
	})
	ids := map[string]bool{}
	err := c.Receipts(context.Background(), now.Add(-time.Hour), func(r app.Receipt) error {
		if ids[r.ID] {
			t.Fatal("duplicate identity")
		}
		ids[r.ID] = true
		return nil
	})
	if err != nil || pages != 2 || len(ids) != 101 {
		t.Fatalf("pages %d receipts %d err %v", pages, len(ids), err)
	}
}
func TestInvalidOrRepeatedPagesFailClosed(t *testing.T) {
	c := fixture(t)
	now := time.Now()
	list := []any{}
	for i := 0; i < 100; i++ {
		list = append(list, map[string]any{"user_id": 1234567, "item_id": 102201, "item_num": 1, "time": now.UnixMilli()})
	}
	c.http.Transport = transport(func(*http.Request) (*http.Response, error) {
		return response(map[string]any{"code": 0, "data": map[string]any{"list": list}}), nil
	})
	if err := c.Receipts(context.Background(), now.Add(-time.Hour), func(app.Receipt) error { return nil }); err == nil {
		t.Fatal("accepted repeated page")
	}
	c.http.Transport = transport(func(*http.Request) (*http.Response, error) {
		return response(map[string]any{"data": map[string]any{"list": []any{}}}), nil
	})
	if err := c.Receipts(context.Background(), now.Add(-time.Hour), func(app.Receipt) error { return nil }); err == nil {
		t.Fatal("accepted missing success code")
	}
}

func TestDatabaseCredentialClientUsesTokenWithoutFile(t *testing.T) {
	c, e := NewCredentialClient("https://example.invalid", "1234567", "database-test-token", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("token") != "database-test-token" {
			t.Fatal("token not injected")
		}
		return response(map[string]any{"code": 0, "data": map[string]any{}}), nil
	})
	if _, e = c.call(context.Background(), "GET", "/account/social?user_id=7654321", nil); e != nil {
		t.Fatal(e)
	}
}

func TestTokenInvalidHTTPStatus(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		c := fixture(t)
		c.http.Transport = transport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		})
		_, err := c.call(context.Background(), "GET", "/player/transfer/log", nil)
		if errors.Is(err, app.ErrTokenInvalid) != (status == 401 || status == 403) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
