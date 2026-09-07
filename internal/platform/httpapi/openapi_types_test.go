package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/application/auth"
	"github.com/block-beast/platform/internal/application/chain"
	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/application/credit"
	"github.com/block-beast/platform/internal/application/currency"
	"github.com/block-beast/platform/internal/application/leaderboard"
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/application/redpacket"
	"github.com/block-beast/platform/internal/application/task"
	"github.com/block-beast/platform/internal/application/uploads"
	"github.com/block-beast/platform/internal/domain/game"
)

// 只读取当前仓库约定的顶层 component/property 缩进，不把它当通用 YAML 解析器。
// YAML 完整语法与 $ref 校验另行执行；这里用真实 Go 类型防止 ID/金额类型回归。
func contractComponent(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "    "+name+":\n")
	if start < 0 {
		t.Fatalf("missing schema %s", name)
	}
	block := text[start:]
	if end := regexp.MustCompile(`(?m)^    [A-Za-z][A-Za-z0-9_]*:`).FindAllStringIndex(block, -1); len(end) > 1 {
		block = block[:end[1][0]]
	}
	return block
}
func contractProperty(t *testing.T, schema, field string) string {
	t.Helper()
	block := contractComponent(t, schema)
	start := regexp.MustCompile(`(?m)^        ` + regexp.QuoteMeta(field) + `:`).FindStringIndex(block)
	if start == nil {
		t.Errorf("%s.%s missing from OpenAPI", schema, field)
		return ""
	}
	tail := block[start[0]:]
	matches := regexp.MustCompile(`(?m)^        [A-Za-z_][A-Za-z0-9_]*:`).FindAllStringIndex(tail, -1)
	if len(matches) > 1 {
		tail = tail[:matches[1][0]]
	}
	return tail
}
func checkContractType(t *testing.T, schema, field, expected string) {
	t.Helper()
	if strings.HasSuffix(field, "_minor") {
		field = strings.TrimSuffix(field, "_minor")
		expected = "string"
	}
	if field == "cost_balance" || field == "reward_balance" {
		expected = "string"
	}
	p := contractProperty(t, schema, field)
	match := regexp.MustCompile(`(?m)^          type:\s*(\[[^\]]+\]|(?:- (?:string|number)\s*)+|[A-Za-z]+)`).FindStringSubmatch(p)
	if len(match) < 2 || !regexp.MustCompile(`\b`+expected+`\b`).MatchString(match[1]) {
		t.Errorf("%s.%s: Go expects %s, schema=%s", schema, field, expected, p)
	}
}
func TestOpenAPIComponentTypesMatchGo(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		publicIDs bool
	}{
		{"LoginResult", auth.LoginResult{}, false},
		{"ChatRoom", chat.Room{}, false},
		{"ChatMessageSender", chat.MessageSender{}, false},
		{"Upload", uploads.Upload{}, true},
		{"RedPacket", redpacket.Packet{}, true},
		{"RedPacketClaim", redpacket.Claim{}, true},
		{"GameTypeInput", operations.GameTypeInput{}, false},
		{"CustomerPhraseInput", operations.PhraseInput{}, false},
		{"CustomerPhraseCreate", operations.PhraseInput{}, false},
		{"CustomerPhrase", operations.Phrase{}, false},
		{"GameRoomInput", operations.GameRoomInput{}, false},
		{"BetTask", task.BetTask{}, false},
		{"DepositResult", chain.DepositResult{}, false},
		{"ConsumeStaminaRequest", credit.ConsumeStaminaInput{}, false},
		{"AdminCreditRequest", credit.AdminCreditInput{}, false},
		{"WalletAdjustmentRequest", credit.AdjustmentInput{}, false},
		{"CurrencyUpdate", currency.Update{}, false},
		{"Currency", currency.Currency{}, false},
		{"BetTaskConfig", task.BetTaskConfig{}, false},
		{"SpinPrize", credit.SpinPrize{}, false},
		{"SpinConfig", credit.SpinConfig{}, false},
		{"HashCurrencyConfig", operations.HashCurrencyConfig{}, false},
		{"HashRoomConfig", operations.HashRoomConfig{}, false},
		{"HashConfigUpdate", operations.HashConfigUpdate{}, false},
		{"LeaderboardRewardRule", leaderboard.RewardRule{}, false},
		{"LeaderboardRewardRuleSet", leaderboard.RewardRuleSet{}, false},
		{"DepositWebhookRequest", chain.PQPADepositWebhook{}, false},
		{"CreditResult", credit.CreditResult{}, true},
		{"WalletAdjustmentResult", credit.AdjustmentResult{}, true},
		{"ConsumeResult", credit.ConsumeResult{}, true},
		{"WalletBalanceInfo", credit.BalanceInfo{}, true},
		{"LedgerEntry", credit.LedgerEntry{}, true},
		{"UnifiedLedgerEntry", credit.UnifiedLedgerEntry{}, false},
		{"PointWithdrawal", credit.PointWithdrawal{}, true},
		{"SpinResult", credit.SpinResult{}, false},
		{"BetTaskClaim", task.BetTaskClaim{}, false},
		{"HashConfig", operations.HashConfig{}, false},
		{"HashTrend", game.HashTrend{}, false},
		{"HashTrendStreak", game.HashTrendStreak{}, false},
		{"Round", game.Round{}, false},
		{"AdminCurrentBet", operations.MonitorBet{}, false},
		{"RefundClearanceRecord", operations.RefundClearanceRecord{}, false},
		{"AdminUser", operations.User{}, false},
		{"PlatformConfig", operations.PlatformConfig{}, false},
		{"Withdrawal", chain.Withdrawal{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.TypeOf(tc.value)
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "-" || name == "" {
					continue
				}
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft == reflect.TypeOf(json.RawMessage{}) {
					continue
				}
				expected := ""
				switch ft.Kind() {
				case reflect.String:
					expected = "string"
				case reflect.Int, reflect.Int64:
					expected = "integer"
				case reflect.Bool:
					expected = "boolean"
				case reflect.Slice, reflect.Array:
					expected = "array"
				case reflect.Map, reflect.Struct:
					expected = "object"
				}
				if ft == reflect.TypeOf(time.Time{}) {
					expected = "string"
				}
				if tc.publicIDs && isPublicUserIDField(name) {
					expected = "integer"
				}
				// The shared public codec exposes hash odds as actual decimal
				// strings, while application structs retain exact fractions.
				if tc.name == "HashCurrencyConfig" {
					if strings.HasSuffix(name, "_divisor") {
						continue
					}
					if strings.HasSuffix(name, "_multiplier") {
						name = strings.TrimSuffix(name, "_multiplier") + "_rate"
						expected = "string"
					}
				}
				if expected != "" {
					checkContractType(t, tc.name, name, expected)
				}
			}
		})
	}
}

func TestOpenAPIInlineRequestTypesMatchHandlers(t *testing.T) {
	for _, tc := range []struct{ file, handler, schema string }{
		{"server.go", "register", "RegisterRequest"}, {"server.go", "loginForAudience", "LoginRequest"},
		{"currency.go", "createCurrency", "CurrencyCreate"},
		{"server.go", "placeBet", "PlaceBetRequest"}, {"withdrawal_request.go", "requestWithdrawal", "WithdrawalRequest"},
		{"credit.go", "requestPointWithdrawal", "PointWithdrawalRequest"}, {"agent.go", "grantCommission", "CommissionGrantRequest"},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != tc.handler {
					continue
				}
				ast.Inspect(fn, func(n ast.Node) bool {
					v, ok := n.(*ast.ValueSpec)
					if !ok {
						return true
					}
					st, ok := v.Type.(*ast.StructType)
					if !ok {
						return true
					}
					found = true
					for _, f := range st.Fields.List {
						if f.Tag == nil {
							continue
						}
						tag, _ := strconv.Unquote(f.Tag.Value)
						name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
						fieldType := f.Type
						if pointer, ok := fieldType.(*ast.StarExpr); ok {
							fieldType = pointer.X
						}
						typ, ok := fieldType.(*ast.Ident)
						if !ok {
							continue
						}
						expected := map[string]string{"string": "string", "int64": "integer", "int": "integer", "bool": "boolean"}[typ.Name]
						if expected != "" {
							checkContractType(t, tc.schema, name, expected)
						}
					}
					return false
				})
			}
			if !found {
				t.Fatalf("no inline request struct in %s", tc.handler)
			}
		})
	}
}

func TestPublicUserPathParametersAreNotUUIDs(t *testing.T) {
	data, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`(?m)- name: (?:userID|agentID)\n(?:[^\n]*\n){0,4}[^\n]*schema: \{ type: string, format: uuid \}`)
	if matches := pattern.FindAllString(string(data), -1); len(matches) > 0 {
		t.Fatalf("public IDs incorrectly documented as UUIDs: %v", matches)
	}
}

func TestOpenAPIAmountPatternsAcceptDecimalStrings(t *testing.T) {
	for _, name := range []string{"AdminCreditRequest", "WalletAdjustmentRequest"} {
		property := contractProperty(t, name, "amount")
		match := regexp.MustCompile(`pattern: (.+)`).FindStringSubmatch(property)
		if len(match) != 2 {
			t.Fatalf("missing amount pattern for %s", name)
		}
		patternText := strings.TrimSpace(match[1])
		if strings.HasPrefix(patternText, "\"") {
			var err error
			patternText, err = strconv.Unquote(patternText)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			patternText = strings.Trim(patternText, "'")
		}
		pattern, err := regexp.Compile(patternText)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"1", "1.5", "100.001"} {
			if !pattern.MatchString(value) {
				t.Errorf("%s rejects %q", name, value)
			}
		}
		for _, value := range []string{"-1", "1e3", "1x5"} {
			if pattern.MatchString(value) {
				t.Errorf("%s accepts %q", name, value)
			}
		}
	}
}
