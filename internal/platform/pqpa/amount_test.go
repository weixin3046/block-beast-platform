package pqpa

import "testing"

func TestMinorAmountConversionIsExact(t *testing.T) {
	tests := []struct {
		minor    int64
		decimals int
		decimal  string
	}{
		{1, 6, "0.000001"},
		{10_500_000, 6, "10.500000"},
		{100, 0, "100"},
	}
	for _, test := range tests {
		formatted, err := FormatMinorAmount(test.minor, test.decimals)
		if err != nil || formatted != test.decimal {
			t.Fatalf("FormatMinorAmount(%d, %d) = %q, %v", test.minor, test.decimals, formatted, err)
		}
		parsed, err := ParseMinorAmount(formatted, test.decimals)
		if err != nil || parsed != test.minor {
			t.Fatalf("ParseMinorAmount(%q, %d) = %d, %v", formatted, test.decimals, parsed, err)
		}
	}
}

func TestParseMinorAmountRejectsPrecisionLossAndOverflow(t *testing.T) {
	for _, value := range []string{"1.0000001", "-1", "1e6", "9223372036854775808"} {
		if _, err := ParseMinorAmount(value, 6); err == nil {
			t.Fatalf("ParseMinorAmount(%q) should fail", value)
		}
	}
}
