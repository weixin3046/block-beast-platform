package chain

import "testing"

func TestPlatformDepositPrecision(t *testing.T) {
	for _, tt := range []struct {
		raw             string
		chain, platform int
		want            int64
		invalid         bool
	}{
		{"100.000000000000000000", 18, 6, 100000000, false},
		{"1.500000", 6, 3, 1500, false},
		{"0.0000001", 18, 6, 0, true},
		{"1.0001", 3, 6, 0, true},
		{"0", 18, 6, 0, true},
		{"1e3", 18, 6, 0, true},
		{"9223372036854.775808", 18, 6, 0, true},
	} {
		got, err := parsePlatformDeposit(tt.raw, tt.chain, tt.platform)
		if (err != nil) != tt.invalid || got != tt.want {
			t.Fatalf("%+v got %d %v", tt, got, err)
		}
	}
}
