package pqpa

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

var ErrInvalidDecimalAmount = errors.New("invalid decimal amount")

func FormatMinorAmount(minor int64, decimals int) (string, error) {
	if minor < 0 || decimals < 0 || decimals > 18 {
		return "", ErrInvalidDecimalAmount
	}
	if decimals == 0 {
		return strconv.FormatInt(minor, 10), nil
	}
	scale := int64(1)
	for range decimals {
		scale *= 10
	}
	whole, fraction := minor/scale, minor%scale
	return strconv.FormatInt(whole, 10) + "." + leftPad(strconv.FormatInt(fraction, 10), decimals), nil
}

func ParseMinorAmount(value string, decimals int) (int64, error) {
	if decimals < 0 || decimals > 18 {
		return 0, ErrInvalidDecimalAmount
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, ErrInvalidDecimalAmount
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, ErrInvalidDecimalAmount
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > decimals {
		return 0, ErrInvalidDecimalAmount
	}
	for _, part := range parts {
		if part == "" && decimals != 0 {
			continue
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0, ErrInvalidDecimalAmount
			}
		}
	}
	scale := int64(1)
	for range decimals {
		scale *= 10
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/scale {
		return 0, ErrInvalidDecimalAmount
	}
	fraction += strings.Repeat("0", decimals-len(fraction))
	fractionMinor := int64(0)
	if fraction != "" {
		fractionMinor, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, ErrInvalidDecimalAmount
		}
	}
	if whole*scale > math.MaxInt64-fractionMinor {
		return 0, ErrInvalidDecimalAmount
	}
	return whole*scale + fractionMinor, nil
}

func leftPad(value string, length int) string {
	if len(value) >= length {
		return value
	}
	return strings.Repeat("0", length-len(value)) + value
}
