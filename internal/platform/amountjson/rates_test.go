package amountjson

import (
	"context"
	"strings"
	"testing"
)

func TestHashRatesActualDecimalsRoundTrip(t *testing.T) {
	v, e := ReadJSON(strings.NewReader(`{"guess_multiplier":9350,"guess_divisor":1000,"dodge_multiplier":1080,"dodge_divisor":1000,"road_multiplier":1985,"road_divisor":1000}`))
	if e != nil {
		t.Fatal(e)
	}
	c := New(context.Background(), nil)
	if e = c.Convert(v, "", false, false); e != nil {
		t.Fatal(e)
	}
	m := v.(map[string]any)
	for k, want := range map[string]string{"guess_rate": "9.35", "dodge_rate": "1.08", "road_rate": "1.985"} {
		if m[k] != want {
			t.Fatalf("%s=%v", k, m[k])
		}
	}
	if len(m) != 3 {
		t.Fatal(m)
	}
	if e = c.Convert(v, "", true, false); e != nil {
		t.Fatal(e)
	}
	if e = c.Convert(v, "", false, false); e != nil {
		t.Fatal(e)
	}
	if m["road_rate"] != "1.985" {
		t.Fatal(m)
	}
	for _, raw := range []string{`{"guess_rate":0}`, `{"guess_rate":-1}`, `{"guess_rate":1e3}`, `{"guess_rate":null}`, `{"guess_multiplier":9350}`, `{"guess_divisor":1000}`, `{"road_rate":"1.0000000000000000001"}`} {
		x, _ := ReadJSON(strings.NewReader(raw))
		if e = c.Convert(x, "", true, false); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	x, _ := ReadJSON(strings.NewReader(`{"road_rate":1.985}`))
	if e = c.Convert(x, "", true, false); e != nil {
		t.Fatal(e)
	}
}
