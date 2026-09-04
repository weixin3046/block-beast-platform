package usermessage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestChineseMessages(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"invalid request body", "请求参数格式不正确"},
		{"secondary password is incorrect", "二级密码不正确"},
		{"currency is not registered", "该币种尚未登记"},
		{"task has not reached its claim threshold today", "今日尚未达到该任务的领取条件"},
		{"invalid game rules: outcomes must not contain duplicates", "游戏规则无效：结果选项不能重复"},
		{"amount must be positive: amount allows at most 3 decimal places", "金额必须大于 0，且最多保留 3 位小数"},
		{"task config not found: private-id", "活动任务不存在"},
		{"invalid request body: password=secret", "请求参数格式不正确"},
		{"请求参数格式不正确", "请求参数格式不正确"},
		{"ERROR SQLSTATE 23503: password=secret", Fallback},
		{"数据库错误: password=secret", Fallback},
	} {
		if got := Chinese(tt.input); got != tt.want {
			t.Errorf("%q got %q want %q", tt.input, got, tt.want)
		}
	}
	for source, value := range messages {
		if strings.IndexFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) < 0 {
			t.Errorf("non-Chinese message %q = %q", source, value)
		}
	}
}

// New literal HTTP/Socket error messages must be registered, not silently rely
// on the generic fallback intended for unpredictable infrastructure errors.
func TestTransportErrorMessagesHaveTranslations(t *testing.T) {
	for _, dir := range []string{"../httpapi", "../realtime"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				pair, ok := node.(*ast.KeyValueExpr)
				if !ok {
					return true
				}
				isError := false
				switch key := pair.Key.(type) {
				case *ast.BasicLit:
					isError = key.Value == `"error"`
				case *ast.Ident:
					isError = key.Name == "Error"
				}
				if !isError {
					return true
				}
				literal, ok := pair.Value.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				message, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				if message != "" {
					if _, ok := Lookup(message); !ok {
						t.Errorf("%s: missing translation: %q", fs.Position(literal.Pos()), message)
					}
				}
				return true
			})
		}
	}
}
