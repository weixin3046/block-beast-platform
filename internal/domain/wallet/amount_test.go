package wallet

import (
	"errors"
	"testing"
)

func TestParseDisplayAmount(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		decimals int
		want     int64
		wantErr  bool
	}{
		{name: "hundred points", value: "100", decimals: 3, want: 100000},
		{name: "fractional points", value: "1.5", decimals: 3, want: 1500},
		{name: "smallest points", value: "0.001", decimals: 3, want: 1},
		{name: "integer stamina", value: "8", decimals: 0, want: 8},
		{name: "too many decimals", value: "1.0001", decimals: 3, wantErr: true},
		{name: "zero", value: "0", decimals: 3, wantErr: true},
		{name: "negative", value: "-1", decimals: 3, wantErr: true},
		{name: "exponent", value: "1e3", decimals: 3, wantErr: true},
		{name: "overflow", value: "9223372036854775.808", decimals: 3, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseDisplayAmount(test.value, test.decimals)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidDisplayAmount) {
					t.Fatalf("error = %v, want ErrInvalidDisplayAmount", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ParseDisplayAmount(%q, %d) = %d, %v; want %d", test.value, test.decimals, got, err, test.want)
			}
		})
	}
}

func TestFormatDisplayAmount(t *testing.T) {
	for _, tt := range []struct {
		minor    int64
		decimals int
		want     string
	}{
		{100000, 3, "100.000"}, {1, 6, "0.000001"}, {0, 0, "0"}, {-9223372036854775808, 3, "-9223372036854775.808"},
	} {
		got, err := FormatDisplayAmount(tt.minor, tt.decimals)
		if err != nil || got != tt.want {
			t.Fatalf("got %q %v want %q", got, err, tt.want)
		}
	}
}
