package realtime

import (
	"context"
	"github.com/block-beast/platform/internal/application/currency"
	"github.com/nats-io/nats.go"
	"strings"
	"testing"
)

type moneyCatalog struct{}

func (moneyCatalog) List(context.Context, bool) ([]currency.Currency, error) {
	return []currency.Currency{{Code: "POINTS", Decimals: 3, Enabled: true}}, nil
}
func TestSocketMoneyIsFormattedAfterPrivateRouting(t *testing.T) {
	h := NewHub("", nil).WithCurrencies(moneyCatalog{})
	owner, other := newClient(nil), newClient(nil)
	h.clients["owner"] = map[*client]struct{}{owner: {}}
	h.clients["other"] = map[*client]struct{}{other: {}}
	h.publish(&nats.Msg{Subject: "wallet.ledger.committed", Data: []byte(`{"user_id":"owner","currency":"POINTS","amount_minor":1500,"available_after_minor":98500}`)})
	select {
	case data := <-owner.outbound:
		if strings.Contains(string(data), "_minor") || !strings.Contains(string(data), `"amount":"1.500"`) || !strings.Contains(string(data), `"available_after":"98.500"`) {
			t.Fatal(string(data))
		}
	default:
		t.Fatal("owner event missing")
	}
	select {
	case <-other.outbound:
		t.Fatal("private amount leaked")
	default:
	}
	payload := publicEventPayload("game.round.settled", []byte(`{"round_id":"r","payout_minor":9000,"won_bet_count":1}`))
	if strings.Contains(string(payload), "payout") || !strings.Contains(string(payload), "won_bet_count") {
		t.Fatal(string(payload))
	}
}
