package amountjson

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOutputTrimsOnlyMoneyFractionZeros(t *testing.T) {
	v := map[string]any{"available": "2223.000000", "frozen": "0.000000", "amount": "-1.500000", "stake": "0.000001", "total_bet": "10.0100", "payout_rate": "1.9400", "selection": map[string]any{"amount": "1.500"}, "display_name": "123.000"}
	if e := New(context.Background(), nil).Convert(v, "", false, false); e != nil {
		t.Fatal(e)
	}
	for k, want := range map[string]string{"available": "2223", "frozen": "0", "amount": "-1.5", "stake": "0.000001", "total_bet": "10.01", "payout_rate": "1.9400", "display_name": "123.000"} {
		if v[k] != want {
			t.Fatalf("%s=%v want %s", k, v[k], want)
		}
	}
	b, _ := json.Marshal(v["selection"])
	if string(b) != `{"amount":"1.500"}` {
		t.Fatal(string(b))
	}
}
