package settlement

import (
	"math"
	"testing"
)

func TestCalculateCommission(t *testing.T) {
	tests := []struct {
		stake int64
		rate  int
		want  int64
	}{
		{stake: 10_000, rate: 500, want: 500},
		{stake: 99, rate: 500, want: 4},
		{stake: math.MaxInt64, rate: 10_000, want: math.MaxInt64},
	}
	for _, test := range tests {
		if got := calculateCommission(test.stake, test.rate); got != test.want {
			t.Fatalf("calculateCommission(%d, %d) = %d, want %d", test.stake, test.rate, got, test.want)
		}
	}
}
