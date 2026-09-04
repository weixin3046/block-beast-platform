package wallet

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var ErrInvalidDisplayAmount = errors.New("invalid display amount")
var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)

func ValidDisplayAmount(value string) bool {
	value = strings.TrimSpace(value)
	return decimalPattern.MatchString(value) && strings.Trim(value, "0.") != ""
}

// FormatDisplayAmount never uses floating-point math, including for MinInt64.
func FormatDisplayAmount(minor int64, decimals int) (string, error) {
	if decimals < 0 || decimals > 18 {
		return "", ErrInvalidDisplayAmount
	}
	digits := strconv.FormatInt(minor, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	if decimals == 0 {
		return sign + digits, nil
	}
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	return sign + digits[:len(digits)-decimals] + "." + digits[len(digits)-decimals:], nil
}

// ParseDisplayAmount converts a positive base-unit decimal string to the
// integer minor-unit representation used by wallets and ledgers. Exponents,
// signs, rounding and excess decimal places are deliberately rejected.
func ParseDisplayAmount(value string, decimals int) (int64, error) {
	value = strings.TrimSpace(value)
	if !ValidDisplayAmount(value) || decimals < 0 || decimals > 18 {
		return 0, ErrInvalidDisplayAmount
	}

	parts := strings.SplitN(value, ".", 2)
	integer := parts[0]
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if integer == "" {
		integer = "0"
	}
	if !decimalDigits(integer) || !decimalDigitsOrEmpty(fraction) || len(fraction) > decimals {
		return 0, ErrInvalidDisplayAmount
	}

	digits := strings.TrimLeft(integer+fraction+strings.Repeat("0", decimals-len(fraction)), "0")
	if digits == "" {
		return 0, ErrInvalidDisplayAmount
	}
	minor, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || minor <= 0 {
		return 0, ErrInvalidDisplayAmount
	}
	return minor, nil
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func decimalDigitsOrEmpty(value string) bool {
	return value == "" || decimalDigits(value)
}
