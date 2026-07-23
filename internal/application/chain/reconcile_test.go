package chain

import "testing"

func TestNormalizeProviderWithdrawalStatus(t *testing.T) {
	tests := map[string]string{
		"confirmed":  "confirmed",
		"completed":  "confirmed",
		"success":    "confirmed",
		"failed":     "failed",
		"rejected":   "failed",
		"processing": "",
	}
	for input, expected := range tests {
		if got := normalizeProviderWithdrawalStatus(input); got != expected {
			t.Fatalf("normalizeProviderWithdrawalStatus(%q) = %q, want %q", input, got, expected)
		}
	}
}
