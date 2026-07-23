package chain

import (
	"math"
	"strconv"
	"strings"
)

func parseDecimalMinor(value string, decimals int) (int64, error) {
	if decimals < 0 || decimals > 18 {
		return 0, ErrInvalidAmount
	}
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, ErrInvalidAmount
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > decimals {
		return 0, ErrInvalidAmount
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0, ErrInvalidAmount
			}
		}
	}
	scale := int64(1)
	for range decimals {
		scale *= 10
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/scale {
		return 0, ErrInvalidAmount
	}
	fraction += strings.Repeat("0", decimals-len(fraction))
	var tail int64
	if fraction != "" {
		tail, err = strconv.ParseInt(fraction, 10, 64)
	}
	if err != nil || whole*scale > math.MaxInt64-tail {
		return 0, ErrInvalidAmount
	}
	return whole*scale + tail, nil
}
