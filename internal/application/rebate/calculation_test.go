package rebate

import (
	"math"
	"reflect"
	"testing"
)

func TestCalculate(t *testing.T) {
	tests := []struct {
		name, mode    string
		stake, payout int64
		chain         []Ancestor
		want          []int64
	}{
		{"three increasing levels", "road", 1000000, 0, []Ancestor{{UserID: "a", Level: 1, RatePerMille: 14}, {UserID: "b", Level: 2, RatePerMille: 16}, {UserID: "c", Level: 3, RatePerMille: 20}}, []int64{14000, 2000, 4000}},
		{"dodge net win", "dodge", 100000, 108000, []Ancestor{{Level: 1, RatePerMille: 14}}, []int64{112}},
		{"dodge loss", "dodge", 100000, 0, []Ancestor{{Level: 1, RatePerMille: 14}}, nil},
		{"skip equal and lower levels", "guess", 1000000, 0, []Ancestor{{Level: 2, RatePerMille: 16}, {Level: 2, RatePerMille: 16}, {Level: 1, RatePerMille: 14}, {Level: 3, RatePerMille: 20}}, []int64{16000, 4000}},
		{"virtual intermediary", "road", 1000000, 0, []Ancestor{{Level: 1, RatePerMille: 14, IsVirtual: true}, {Level: 2, RatePerMille: 16}}, []int64{16000}},
		{"round down", "road", 1, 0, []Ancestor{{Level: 1, RatePerMille: 1}}, nil},
		{"maximum without overflow", "road", math.MaxInt64, 0, []Ancestor{{Level: 1, RatePerMille: 1000}}, []int64{math.MaxInt64}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Calculate(tt.stake, tt.payout, tt.mode, tt.chain)
			if err != nil {
				t.Fatal(err)
			}
			var amounts []int64
			for _, e := range got {
				amounts = append(amounts, e.AmountMinor)
			}
			if !reflect.DeepEqual(amounts, tt.want) {
				t.Fatalf("amounts %v, want %v", amounts, tt.want)
			}
		})
	}
}
