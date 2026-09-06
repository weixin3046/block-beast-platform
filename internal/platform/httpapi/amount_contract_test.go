package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/block-beast/platform/internal/application/currency"
	"github.com/block-beast/platform/internal/config"
)

type amountTestCurrencies struct{ stubCurrencies }

func (*amountTestCurrencies) List(context.Context, bool) ([]currency.Currency, error) {
	return []currency.Currency{{Code: "USDT", Decimals: 6, Enabled: true}, {Code: "POINTS", Decimals: 3, Enabled: true}, {Code: "JADE", Decimals: 3, Enabled: true}, {Code: "STAMINA", Decimals: 0, Enabled: true}, {Code: "CUSTOM", Decimals: 2, Enabled: true}}, nil
}
func newAmountTestServer(cfg config.Config, logger *slog.Logger, placer BetPlacer, readiness ReadinessChecker, wallets WalletReader, rounds RoundReader, bets BetReader, canceller RoundCanceller, opts ...Option) *Server {
	return New(cfg, logger, placer, readiness, wallets, rounds, bets, canceller, append([]Option{WithCurrencies(&amountTestCurrencies{})}, opts...)...)
}

func TestAmountContractConversions(t *testing.T) {
	for _, tc := range []struct {
		body, code string
		input      bool
		want       string
	}{
		{`{"currency":"POINTS","stake":100}`, "", true, `"stake_minor":100000`},
		{`{"currency":"POINTS","stake":1.5}`, "", true, `"stake_minor":1500`},
		{`{"currency":"USDT","amount":"9007199254.740993"}`, "", true, `"amount_minor":9007199254740993`},
		{`{"items":[{"accumulation_currency":"USDT","threshold":1.5,"reward_currency":"POINTS","reward":1.5}]}`, "", true, `"threshold_minor":1500000`},
		{`{"cost_currency":"POINTS","cost":1.5,"prizes":[{"currency":"USDT","amount":1.5,"weight":10}]}`, "", true, `"amount_minor":1500000`},
		{`{"currency":"POINTS","rules":[{"reward_currency":"USDT","reward":1.5,"rank_from":1}]}`, "", true, `"reward_minor":1500000`},
		{`{"currency":"POINTS","amount_minor":68,"balance_after_minor":500250}`, "", false, `"balance_after":"500.250"`},
		{`{"currency":"USDT","amount_minor":9007199254740993}`, "", false, `"amount":"9007199254.740993"`},
		{`{"amount_minor":1}`, "STAMINA", false, `"amount":"1"`},
		{`{"bet_limits":{"CUSTOM":{"min_stake":1.5,"max_stake":100}}}`, "", true, `"max_stake_minor":10000`},
		{`{"initial_balances":{"POINTS":1.5,"USDT":100}}`, "", true, `"USDT":100000000`},
	} {
		t.Run(tc.body, func(t *testing.T) {
			c := amountCodec{server: &Server{currencies: &amountTestCurrencies{}}, ctx: context.Background()}
			v, err := readAmountJSON(strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err = c.convert(v, tc.code, tc.input, false); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(v)
			if !strings.Contains(string(b), tc.want) {
				t.Fatalf("got %s want %s", b, tc.want)
			}
			if !tc.input && strings.Contains(string(b), "_minor") {
				t.Fatalf("minor leaked: %s", b)
			}
		})
	}
}

func TestAmountContractRejectsAmbiguousOrInvalidInputs(t *testing.T) {
	for _, body := range []string{`{"currency":"POINTS","stake_minor":100}`, `{"currency":"POINTS","stake":1,"stake_minor":1000}`, `{"currency":"POINTS","stake":1.0001}`, `{"currency":"POINTS","stake":0}`, `{"currency":"POINTS","stake":-1}`, `{"currency":"POINTS","stake":true}`, `{"currency":"POINTS","stake":1e3}`, `{"currency":"MISSING","stake":1}`, `{"currency":"POINTS","stake":9223372036854775807}`} {
		c := amountCodec{server: &Server{currencies: &amountTestCurrencies{}}, ctx: context.Background()}
		v, _ := readAmountJSON(strings.NewReader(body))
		if err := c.convert(v, "", true, false); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestAmountResponseContainsOnlyDisplayMoney(t *testing.T) {
	s := &Server{currencies: &amountTestCurrencies{}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/bets", nil)
	writer := &amountWriter{ResponseWriter: w, codec: amountCodec{server: s, ctx: r.Context()}}
	writeJSON(writer, 200, map[string]any{"currency": "POINTS", "stake_minor": 1500, "payout_minor": 2985, "payout_multiplier": 1985, "payout_divisor": 1000})
	if w.Code != 200 || strings.Contains(w.Body.String(), "_minor") || !strings.Contains(w.Body.String(), `"stake":"1.500"`) || !strings.Contains(w.Body.String(), `"payout_multiplier":1985`) {
		t.Fatal(w.Body.String())
	}
}

func TestEveryMoneyWriteRouteConvertsBeforeHandler(t *testing.T) {
	for _, tc := range []struct{ path, body, want string }{
		{"/v1/bets", `{"currency":"POINTS","stake":1.5}`, `"stake_minor":1500`},
		{"/v1/withdrawals", `{"currency":"USDT","amount":1.5}`, `"amount_minor":1500000`},
		{"/v1/point-withdrawals", `{"amount":1.5}`, `"amount_minor":1500`},
		{"/v1/stamina/consume", `{"amount":2}`, `"amount_minor":2`},
		{"/v1/admin/credits", `{"currency":"POINTS","amount":1.5}`, `"amount":"1.5"`},
		{"/v1/admin/wallet-adjustments", `{"currency":"USDT","amount":1.5}`, `"amount":"1.5"`},
		{"/v1/admin/agents/100001/commissions", `{"currency":"POINTS","amount":1.5}`, `"amount_minor":1500`},
		{"/v1/chat/rooms/room/red-packets", `{"currency":"USDT","total":1.5}`, `"total_minor":1500000`},
		{"/v1/admin/spins", `{"items":[{"cost_currency":"POINTS","cost":1.5,"prizes":[{"currency":"USDT","amount":1.5}]}]}`, `"amount_minor":1500000`},
		{"/v1/admin/tasks/bet-configs", `{"items":[{"accumulation_currency":"USDT","threshold":100,"reward_currency":"POINTS","reward":1.5}]}`, `"threshold_minor":100000000`},
		{"/v1/admin/leaderboard-reward-rules", `{"currency":"USDT","rules":[{"reward_currency":"POINTS","reward":1.5}]}`, `"reward_minor":1500`},
		{"/v1/admin/hash/config", `{"rooms":[{"currency_configs":[{"currency":"POINTS","min_stake":1.5,"road_max_stake":100}]}]}`, `"road_max_stake_minor":100000`},
		{"/v1/admin/virtual-accounts", `{"initial_balances":{"USDT":1.5}}`, `"USDT":1500000`},
		{"/v1/admin/virtual-accounts/100001/automation", `{"currency":"POINTS","stake":1.5}`, `"stake_minor":1500`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s := &Server{currencies: &amountTestCurrencies{}}
			called := false
			handler := s.requestAmounts(func(w http.ResponseWriter, r *http.Request) {
				called = true
				v, err := readAmountJSON(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				b, _ := json.Marshal(v)
				if !strings.Contains(string(b), tc.want) {
					t.Fatalf("got %s want %s", b, tc.want)
				}
			})
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body)))
			if !called {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestDuplicateMoneyFieldsRejected(t *testing.T) {
	for _, body := range []string{`{"stake":1,"stake":2}`, `{"items":[{"reward":1,"reward":2}]}`} {
		if _, err := readAmountJSON(strings.NewReader(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
