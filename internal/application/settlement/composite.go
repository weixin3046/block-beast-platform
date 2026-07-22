package settlement

import (
	"context"

	"github.com/block-beast/platform/internal/domain/game"
)

// CompositeResultSource 按 rules.Source 路由分发到对应的子结果源。
// source 为空或未知时回退到 HashResultSource，保持本地开发与存量数据兼容。
type CompositeResultSource struct {
	tronHash TronHashResultSource
	okxKline OkxKlineResultSource
	fallback HashResultSource
}

// NewCompositeResultSource 创建复合结果源，注入外部数据源配置。
func NewCompositeResultSource(tronRPCURL, okxRESTURL string) CompositeResultSource {
	return CompositeResultSource{
		tronHash: NewTronHashResultSource(tronRPCURL),
		okxKline: NewOkxKlineResultSource(okxRESTURL),
		fallback: NewHashResultSource(),
	}
}

// Outcome 实现 ResultSource 接口，按 rules.Source 路由到对应的子源。
func (composite CompositeResultSource) Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error) {
	switch rules.Source {
	case "tron_hash":
		return composite.tronHash.Outcome(ctx, round, rules)
	case "okx_kline":
		return composite.okxKline.Outcome(ctx, round, rules)
	default:
		return composite.fallback.Outcome(ctx, round, rules)
	}
}
