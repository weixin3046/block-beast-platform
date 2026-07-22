package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidRules = errors.New("invalid game rules")

// Rules 定义单个玩法的结算契约：可选结果池、派奖赔率和中奖判定方式。
// 规则存储在 game_types.rules JSONB 列中，结算时按玩法加载。
type Rules struct {
	// Outcomes 是该玩法可能出现的结果值池，例如 ["red","black"] 或 ["1","2","3","4","5","6"]。
	Outcomes []string `json:"outcomes"`
	// PayoutMultiplier 是中奖投注的派奖倍数（含本金），必须为正整数。
	PayoutMultiplier int64 `json:"payout_multiplier"`
	// MatchField 限定只比较 selection 中指定字段的值（支持点路径，如 "pick.color"）。
	// 为空时保持旧行为：selection 中任一字符串值命中结果即中奖。
	MatchField string `json:"match_field,omitempty"`
	// ResultCount 是开奖时从结果池中选取的结果个数，默认为 1。
	ResultCount int `json:"result_count,omitempty"`
	// Source 标识外部数据源，如 "tron_hash"、"okx_kline"；为空时使用本地哈希回退。
	Source string `json:"source,omitempty"`
	// Extras 存储数据源参数，如 {"base_block_height":N,"block_interval":5} 或 {"symbol":"BTC-USDT"}。
	Extras json.RawMessage `json:"extras,omitempty"`
	// DodgeMode 为 true 时启用躲避玩法判定：选中值不在 outcome 中即赢。
	DodgeMode bool `json:"dodge_mode,omitempty"`
}

// ParseRules 解析并校验 game_types.rules 中的玩法规则。
func ParseRules(raw json.RawMessage) (Rules, error) {
	var rules Rules
	if len(raw) == 0 {
		return rules, fmt.Errorf("%w: rules must not be empty", ErrInvalidRules)
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return rules, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	return rules, rules.Validate()
}

// Validate 校验规则：结果池非空且无空值，赔率为正，开奖个数不超过结果池大小。
func (rules Rules) Validate() error {
	if len(rules.Outcomes) == 0 {
		return fmt.Errorf("%w: outcomes must not be empty", ErrInvalidRules)
	}
	seen := make(map[string]struct{}, len(rules.Outcomes))
	for _, outcome := range rules.Outcomes {
		if outcome == "" {
			return fmt.Errorf("%w: outcomes must not contain empty values", ErrInvalidRules)
		}
		if _, duplicated := seen[outcome]; duplicated {
			return fmt.Errorf("%w: outcomes must not contain duplicates", ErrInvalidRules)
		}
		seen[outcome] = struct{}{}
	}
	if rules.PayoutMultiplier <= 0 {
		return fmt.Errorf("%w: payout_multiplier must be positive", ErrInvalidRules)
	}
	if rules.ResultCount < 0 || rules.ResultCount > len(rules.Outcomes) {
		return fmt.Errorf("%w: result_count must be between 1 and the outcome pool size", ErrInvalidRules)
	}
	return nil
}

// DrawCount 返回开奖时应选取的结果个数。
func (rules Rules) DrawCount() int {
	if rules.ResultCount <= 0 {
		return 1
	}
	return rules.ResultCount
}

// SelectionWins 判定一份投注选择是否命中开奖结果。
// 设置 MatchField 时仅比较该字段的值，否则比较 selection 中的所有字符串值。
// DodgeMode 为 true 时判定反转：selection 选中值不在 outcome 中即赢（躲避玩法）。
func (rules Rules) SelectionWins(selection json.RawMessage, outcome []string) bool {
	winning := make(map[string]struct{}, len(outcome))
	for _, value := range outcome {
		winning[value] = struct{}{}
	}
	var value any
	if json.Unmarshal(selection, &value) != nil {
		return false
	}
	values := make([]string, 0)
	if rules.MatchField != "" {
		collectFieldStrings(value, strings.Split(rules.MatchField, "."), &values)
	} else {
		collectAllStrings(value, &values)
	}
	if rules.DodgeMode {
		// 躲避玩法：选中值对应的 dodge_X 不在 outcome 中即赢。
		for _, value := range values {
			if _, ok := winning["dodge_"+value]; !ok {
				return true
			}
		}
		return false
	}
	for _, value := range values {
		if _, ok := winning[value]; ok {
			return true
		}
	}
	return false
}

func collectFieldStrings(value any, path []string, values *[]string) {
	if len(path) == 0 {
		collectAllStrings(value, values)
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	collectFieldStrings(object[path[0]], path[1:], values)
}

func collectAllStrings(value any, values *[]string) {
	switch typed := value.(type) {
	case string:
		*values = append(*values, typed)
	case []any:
		for _, item := range typed {
			collectAllStrings(item, values)
		}
	case map[string]any:
		for _, item := range typed {
			collectAllStrings(item, values)
		}
	}
}
