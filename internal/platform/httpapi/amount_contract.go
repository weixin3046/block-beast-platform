package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/block-beast/platform/internal/platform/amountjson"
	"io"
	"net/http"
	"strings"
)

type amountCodec struct {
	server *Server
	ctx    context.Context
	inner  *amountjson.Codec
}

func (c *amountCodec) convert(v any, code string, input, adjustment bool) error {
	if c.inner == nil {
		c.inner = amountjson.New(c.ctx, c.server.currencies)
	}
	return c.inner.Convert(v, code, input, adjustment)
}
func readAmountJSON(r io.Reader) (any, error) { return amountjson.ReadJSON(r) }

func amountRouteCurrency(path string) string {
	if strings.Contains(path, "/point-withdrawals") || strings.HasPrefix(path, "/v1/points/") {
		return "POINTS"
	}
	if strings.HasPrefix(path, "/v1/stamina/") {
		return "STAMINA"
	}
	return ""
}

func amountInputRoute(r *http.Request) bool {
	if r.Method != "POST" && r.Method != "PUT" {
		return false
	}
	p := r.URL.Path
	if p == "/v1/admin/robot-plans" || strings.HasPrefix(p, "/v1/admin/robot-plans/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/admin/spins/") || strings.HasPrefix(p, "/v1/admin/tasks/bet-configs/") {
		return true
	}
	switch p {
	case "/v1/bets", "/v1/withdrawals", "/v1/point-withdrawals", "/v1/stamina/consume", "/v1/admin/credits", "/v1/admin/wallet-adjustments", "/v1/admin/spins", "/v1/admin/tasks/bet-configs", "/v1/admin/leaderboard-reward-rules", "/v1/admin/hash/config", "/v1/admin/virtual-accounts":
		return true
	}
	return strings.HasPrefix(p, "/v1/admin/configs/") || (strings.HasPrefix(p, "/v1/chat/rooms/") && strings.HasSuffix(p, "/red-packets")) || (strings.HasPrefix(p, "/v1/admin/agents/") && strings.HasSuffix(p, "/commissions")) || (strings.HasPrefix(p, "/v1/admin/virtual-accounts/") && strings.HasSuffix(p, "/automation"))
}

func (s *Server) requestAmounts(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if amountInputRoute(r) {
			v, err := readAmountJSON(http.MaxBytesReader(w, r.Body, 1<<20))
			if err == nil {
				c := amountCodec{server: s, ctx: r.Context()}
				err = c.convert(v, amountRouteCurrency(r.URL.Path), true, r.URL.Path == "/v1/admin/credits" || r.URL.Path == "/v1/admin/wallet-adjustments")
			}
			if err != nil {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return
			}
			data, err := json.Marshal(v)
			if err != nil {
				writeJSON(w, 400, map[string]string{"error": "金额参数无效"})
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
		}
		next(w, r)
	}
}

type amountWriter struct {
	http.ResponseWriter
	codec    amountCodec
	currency string
}

func (w *amountWriter) publicAmountJSON(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	obj, err := readAmountJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if err = w.codec.convert(obj, w.currency, false, false); err != nil {
		return nil, err
	}
	return obj, nil
}
func (w *amountWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (s *Server) withAmountResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Chain callbacks are machine contracts and use chain-native units.
		if strings.HasPrefix(r.URL.Path, "/v1/") && !strings.HasPrefix(r.URL.Path, "/v1/webhooks/") {
			w = &amountWriter{ResponseWriter: w, codec: amountCodec{server: s, ctx: r.Context()}, currency: amountRouteCurrency(r.URL.Path)}
		}
		next.ServeHTTP(w, r)
	})
}
