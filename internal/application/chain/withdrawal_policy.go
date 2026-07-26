package chain

import (
	"errors"
	"strings"
	"unicode"
)

type WithdrawalPolicy struct {
	MinimumMinor    int64
	MaximumMinor    int64
	DailyLimitMinor int64
}

func (policy WithdrawalPolicy) Validate() error {
	if policy.MinimumMinor < 0 || policy.MaximumMinor < 0 || policy.DailyLimitMinor < 0 {
		return errors.New("withdrawal limits cannot be negative")
	}
	if policy.MaximumMinor > 0 && policy.MinimumMinor > policy.MaximumMinor {
		return errors.New("withdrawal minimum exceeds per-request maximum")
	}
	if policy.DailyLimitMinor > 0 && policy.MinimumMinor > policy.DailyLimitMinor {
		return errors.New("withdrawal minimum exceeds daily limit")
	}
	return nil
}

func (policy WithdrawalPolicy) validateAmount(amount int64) error {
	if policy.MinimumMinor > 0 && amount < policy.MinimumMinor {
		return ErrWithdrawalBelowMinimum
	}
	if policy.MaximumMinor > 0 && amount > policy.MaximumMinor {
		return ErrWithdrawalAboveMaximum
	}
	return nil
}

func validateWithdrawalDestination(chainCode, address, memo string) error {
	address = strings.TrimSpace(address)
	if len(address) < 8 || len(address) > 256 || containsSpaceOrControl(address) {
		return ErrInvalidWithdrawalAddress
	}
	if len(memo) > 256 || containsControl(memo) {
		return ErrInvalidWithdrawalAddress
	}
	chain := strings.ToUpper(strings.TrimSpace(chainCode))
	switch {
	case chain == "TRON":
		if len(address) != 34 || address[0] != 'T' || !allInAlphabet(address, "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz") {
			return ErrInvalidWithdrawalAddress
		}
	case isEVMChain(chain):
		if len(address) != 42 || !strings.HasPrefix(address, "0x") || !allInAlphabet(address[2:], "0123456789abcdefABCDEF") {
			return ErrInvalidWithdrawalAddress
		}
	}
	return nil
}

func containsSpaceOrControl(value string) bool {
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func allInAlphabet(value, alphabet string) bool {
	for _, character := range value {
		if !strings.ContainsRune(alphabet, character) {
			return false
		}
	}
	return true
}

func isEVMChain(chain string) bool {
	switch chain {
	case "ETH", "ETHEREUM", "BSC", "BNB", "POLYGON", "ARBITRUM", "OPTIMISM", "BASE", "AVALANCHE":
		return true
	default:
		return false
	}
}
