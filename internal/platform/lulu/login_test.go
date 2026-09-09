package lulu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestSMSLoginProtocol(t *testing.T) {
	c := fixture(t)
	c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("token") != "" {
			t.Fatal("login must not require old token")
		}
		b, _ := io.ReadAll(r.Body)
		p, e := c.decrypt(b)
		if e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if json.Unmarshal(p, &v) != nil || v["tel"] != "13800000000" {
			t.Fatal("payload")
		}
		if r.URL.Path == "/account/send_phone_code" {
			return response(map[string]any{"code": 0}), nil
		}
		if v["code"] != "123456" || v["invite_code"] != float64(0) {
			t.Fatal("login payload")
		}
		return response(map[string]any{"code": 0, "data": map[string]any{"token": "test-new-token", "user_id": 1234567}}), nil
	})
	if e := c.SendCode(context.Background(), "13800000000"); e != nil {
		t.Fatal(e)
	}
	uid, tok, e := c.PhoneLogin(context.Background(), "13800000000", "123456")
	if e != nil || uid != "1234567" || tok != "test-new-token" {
		t.Fatal("login result", e)
	}
}
