package settlement

import (
	"errors"
	"fmt"
	"strings"
)

var ErrNoDigitFound = errors.New("no digit (0-9) found in input")

// lastDigit 从字符串末尾向前扫描，返回第一个 0-9 数字字符对应的整数值。
// 自动跳过 0x 前缀、小数点、字母等非数字字符。找不到数字时返回 ErrNoDigitFound。
func lastDigit(input string) (int, error) {
	for index := len(input) - 1; index >= 0; index-- {
		char := input[index]
		if char >= '0' && char <= '9' {
			return int(char - '0'), nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrNoDigitFound, input)
}

// outcomeShape 判定玩法形态，用于将尾数映射为对应的 outcome 值。
type outcomeShape int

const (
	shapeDigit    outcomeShape = iota // 竞猜尾数：outcomes 含 "0".."9"
	shapeBigSmall                     // 大小单双：outcomes 含 "big"
	shapeDodge                        // 躲避：DodgeMode = true
	shapeOddEven                      // 奇偶：outcomes 仅 ["odd","even"]
)

// detectShape 根据 outcomes 内容判定玩法形态。
func detectShape(outcomes []string, dodgeMode bool) outcomeShape {
	if dodgeMode {
		return shapeDodge
	}
	hasDigit := false
	hasBig := false
	for _, outcome := range outcomes {
		if outcome >= "0" && outcome <= "9" && len(outcome) == 1 {
			hasDigit = true
		}
		if outcome == "big" {
			hasBig = true
		}
	}
	if hasBig {
		return shapeBigSmall
	}
	if hasDigit {
		return shapeDigit
	}
	return shapeOddEven
}

// mapOutcome 将尾数映射为对应玩法形态的 outcome 值列表。
func mapOutcome(digit int, shape outcomeShape) []string {
	switch shape {
	case shapeDigit:
		return []string{fmt.Sprintf("%d", digit)}
	case shapeBigSmall:
		return bigSmallOutcome(digit)
	case shapeDodge:
		return []string{fmt.Sprintf("dodge_%d", digit)}
	case shapeOddEven:
		return oddEvenOutcome(digit)
	default:
		return []string{fmt.Sprintf("%d", digit)}
	}
}

// bigSmallOutcome 返回大小和单双两个维度的 outcome 值。
func bigSmallOutcome(digit int) []string {
	bigSmall := "small"
	if digit >= 5 {
		bigSmall = "big"
	}
	oddEven := "even"
	if digit%2 == 1 {
		oddEven = "odd"
	}
	return []string{bigSmall, oddEven}
}

// oddEvenOutcome 返回奇偶维度的 outcome 值。
func oddEvenOutcome(digit int) []string {
	if digit%2 == 1 {
		return []string{"odd"}
	}
	return []string{"even"}
}

// containsOutcome 检查 outcome 列表中是否包含指定值。
func containsOutcome(outcomes []string, target string) bool {
	for _, outcome := range outcomes {
		if strings.EqualFold(outcome, target) {
			return true
		}
	}
	return false
}
