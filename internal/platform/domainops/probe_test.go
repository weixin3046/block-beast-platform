package domainops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeChecksBodyAndDoesNotFollowRedirects(t *testing.T) {
	bad := false
	redirect := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.example.com" {
			t.Errorf("host %s", r.Host)
		}
		if redirect {
			http.Redirect(w, r, "http://127.0.0.1/redirected", 302)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			if bad {
				w.Write([]byte("default site"))
			} else {
				w.Write([]byte(`{"status":"ok"}`))
			}
		case "/readyz":
			w.Write([]byte(`{"status":"ready"}`))
		case "/v1/ws":
			w.WriteHeader(401)
		}
	}))
	defer s.Close()
	client := probeClient(s.Listener.Addr().String(), nil)
	got, err := probeOnce(context.Background(), Domain{Name: "api.example.com"}, client, "")
	if err != nil || got.HTTPS != "not-configured" || got.WebSocket != "route-only" {
		t.Fatalf("%+v %v", got, err)
	}
	bad = true
	if _, err = probeOnce(context.Background(), Domain{Name: "api.example.com"}, client, ""); err == nil {
		t.Fatal("wrong body accepted")
	}
	bad = false
	redirect = true
	if _, err = probeOnce(context.Background(), Domain{Name: "api.example.com"}, client, ""); err == nil {
		t.Fatal("redirect accepted")
	}
}
func TestProbeDisablesProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	c := probeClient("127.0.0.1:1", nil)
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("proxy enabled")
	}
}

func TestOriginProbeRequiresAllowedAndRejectedResponses(t *testing.T) {
	broken := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			if r.Header.Get("Origin") == "https://web.example.com" {
				if !broken {
					w.Header().Set("Access-Control-Allow-Origin", "https://web.example.com")
				}
				w.WriteHeader(204)
			} else {
				w.WriteHeader(403)
			}
			return
		}
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"status":"ok"}`))
		case "/readyz":
			w.Write([]byte(`{"status":"ready"}`))
		case "/v1/ws":
			w.WriteHeader(401)
		}
	}))
	defer s.Close()
	c := probeClient(s.Listener.Addr().String(), nil)
	d := Domain{Name: "api.example.com", Origins: []string{"https://web.example.com"}}
	if _, err := probeOnce(context.Background(), d, c, ""); err != nil {
		t.Fatal(err)
	}
	broken = true
	if _, err := probeOnce(context.Background(), d, c, ""); err == nil {
		t.Fatal("missing allowed origin accepted")
	}
}
