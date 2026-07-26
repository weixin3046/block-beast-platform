package chain

import (
	"errors"
	"testing"
)

func TestWithdrawalPolicyValidatesAmount(t *testing.T) {
	policy := WithdrawalPolicy{MinimumMinor: 100, MaximumMinor: 1000}
	if err := policy.validateAmount(100); err != nil {
		t.Fatalf("minimum boundary: %v", err)
	}
	if err := policy.validateAmount(1000); err != nil {
		t.Fatalf("maximum boundary: %v", err)
	}
	if err := policy.validateAmount(99); !errors.Is(err, ErrWithdrawalBelowMinimum) {
		t.Fatalf("below minimum error = %v", err)
	}
	if err := policy.validateAmount(1001); !errors.Is(err, ErrWithdrawalAboveMaximum) {
		t.Fatalf("above maximum error = %v", err)
	}
}

func TestWithdrawalPolicyRejectsInvalidConfiguration(t *testing.T) {
	if err := (WithdrawalPolicy{MinimumMinor: 200, MaximumMinor: 100}).Validate(); err == nil {
		t.Fatal("minimum above maximum must be rejected")
	}
	if err := (WithdrawalPolicy{MinimumMinor: 200, DailyLimitMinor: 100}).Validate(); err == nil {
		t.Fatal("minimum above daily limit must be rejected")
	}
}

func TestWithdrawalDestinationValidation(t *testing.T) {
	tests := []struct {
		name    string
		chain   string
		address string
		memo    string
		wantErr bool
	}{
		{name: "tron", chain: "TRON", address: "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"},
		{name: "evm", chain: "POLYGON", address: "0x1111111111111111111111111111111111111111"},
		{name: "generic", chain: "SOLANA", address: "genericAddress123"},
		{name: "short", chain: "SOLANA", address: "short", wantErr: true},
		{name: "tron alphabet", chain: "TRON", address: "T0RabPrwbZy45sbavfcjinPJC18kjpRTv8", wantErr: true},
		{name: "evm length", chain: "POLYGON", address: "0x1234", wantErr: true},
		{name: "whitespace", chain: "SOLANA", address: "address with spaces", wantErr: true},
		{name: "memo control", chain: "SOLANA", address: "genericAddress123", memo: "bad\nvalue", wantErr: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateWithdrawalDestination(testCase.chain, testCase.address, testCase.memo)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, testCase.wantErr)
			}
		})
	}
}
