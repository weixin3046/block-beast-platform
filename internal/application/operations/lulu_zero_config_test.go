package operations

import "testing"

func TestLuluZeroConfig(t *testing.T) {
	for _, tc := range []struct {
		m, d, min, max int64
		valid          bool
	}{
		{0, 0, 0, 0, true}, {1180, 1000, 1000, 100000, true},
		{0, 1000, 0, 0, false}, {1180, 0, 1, 100, false}, {1180, 1000, 0, 100, false},
		{1180, 1000, 100, 1, false}, {-1, 0, 0, 0, false},
	} {
		if got := validLuluConfigAmounts(tc.m, tc.d, tc.min, tc.max); got != tc.valid {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}
