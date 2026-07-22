package settlement

import (
	"testing"
)

func TestLastDigit(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"hex hash with digit at end", "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567895", 5, false},
		{"hex hash with digit in middle", "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678a5", 5, false},
		{"price with decimal", "65432.17", 7, false},
		{"price without decimal", "65432", 2, false},
		{"all letters", "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd", 0, true},
		{"empty string", "", 0, true},
		{"single digit", "7", 7, false},
		{"digit zero", "0xabc0", 0, false},
		{"digit nine", "0xabc9", 9, false},
		{"trailing non-digit", "0x1234abcdef", 4, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lastDigit(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("lastDigit(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("lastDigit(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectShape(t *testing.T) {
	tests := []struct {
		name      string
		outcomes  []string
		dodgeMode bool
		want      outcomeShape
	}{
		{"digit outcomes", []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}, false, shapeDigit},
		{"big small outcomes", []string{"small", "big", "odd", "even"}, false, shapeBigSmall},
		{"dodge mode", []string{"dodge_0", "dodge_1"}, true, shapeDodge},
		{"odd even only", []string{"odd", "even"}, false, shapeOddEven},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectShape(tt.outcomes, tt.dodgeMode)
			if got != tt.want {
				t.Fatalf("detectShape(%v, %v) = %v, want %v", tt.outcomes, tt.dodgeMode, got, tt.want)
			}
		})
	}
}

func TestMapOutcome(t *testing.T) {
	tests := []struct {
		name  string
		digit int
		shape outcomeShape
		want  []string
	}{
		{"digit 5", 5, shapeDigit, []string{"5"}},
		{"digit 0", 0, shapeDigit, []string{"0"}},
		{"big small 5", 5, shapeBigSmall, []string{"big", "odd"}},
		{"big small 4", 4, shapeBigSmall, []string{"small", "even"}},
		{"big small 0", 0, shapeBigSmall, []string{"small", "even"}},
		{"big small 9", 9, shapeBigSmall, []string{"big", "odd"}},
		{"dodge 5", 5, shapeDodge, []string{"dodge_5"}},
		{"odd even 5", 5, shapeOddEven, []string{"odd"}},
		{"odd even 4", 4, shapeOddEven, []string{"even"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapOutcome(tt.digit, tt.shape)
			if len(got) != len(tt.want) {
				t.Fatalf("mapOutcome(%d, %v) = %v, want %v", tt.digit, tt.shape, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("mapOutcome(%d, %v)[%d] = %q, want %q", tt.digit, tt.shape, i, got[i], tt.want[i])
				}
			}
		})
	}
}
