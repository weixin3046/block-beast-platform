package realtime

import (
	"github.com/nats-io/nats.go"
	"strings"
	"testing"
)

func TestPersonalBetSettlementMoneyAndIsolation(t *testing.T) {
	h := NewHub("", nil).WithCurrencies(moneyCatalog{})
	owner, other := newClient(nil), newClient(nil)
	h.add("owner", owner)
	h.add("other", other)
	h.publish(&nats.Msg{Subject: "game.bet.settled", Data: []byte(`{"user_ids":["owner"],"bets":[{"currency":"POINTS","status":"lost","stake_minor":10000,"payout_minor":0,"net_win_minor":-10000}],"totals":[{"currency":"POINTS","stake_minor":10000,"payout_minor":0,"net_win_minor":-10000}]}`)})
	if len(other.outbound) != 0 || len(owner.outbound) != 1 {
		t.Fatal("incorrect private delivery")
	}
	s := string(<-owner.outbound)
	for _, want := range []string{`"stake":"10"`, `"payout":"0"`, `"net_win":"-10"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "_minor") || strings.Contains(s, "user_ids") {
		t.Fatal("internal fields leaked", s)
	}
}
