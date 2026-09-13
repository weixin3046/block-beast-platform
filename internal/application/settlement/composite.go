package settlement

import (
	"context"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CompositeResultSource 按 rules.Source 路由分发到对应的子结果源。
// source 为空或未知时回退到 HashResultSource，保持本地开发与存量数据兼容。
type CompositeResultSource struct {
	tronHash  TronHashResultSource
	okxKline  OkxKlineResultSource
	fallback  HashResultSource
	okxStream *OkxKlineStream
	lulu      LuluResultSource
}

func (composite *CompositeResultSource) WithLulu(pool *pgxpool.Pool) *CompositeResultSource {
	composite.lulu = NewLuluResultSource(pool)
	return composite
}

// NewCompositeResultSource 创建复合结果源，注入外部数据源配置。
func NewCompositeResultSource(tronGridAPIKey, okxRESTURL string) CompositeResultSource {
	return CompositeResultSource{
		tronHash: NewTronHashResultSource(tronGridAPIKey),
		okxKline: NewOkxKlineResultSource(okxRESTURL),
		fallback: NewHashResultSource(),
	}
}

func NewCompositeResultSourceWithWebSocket(tronGridAPIKey, tronGridGRPCEndpoint, okxRESTURL, okxWebSocketURL string) *CompositeResultSource {
	stream := NewOkxKlineStream(okxWebSocketURL)
	return &CompositeResultSource{
		tronHash:  NewTronHashResultSourceWithGRPC(tronGridAPIKey, tronGridGRPCEndpoint),
		okxKline:  NewOkxKlineResultSourceWithStream(okxRESTURL, stream),
		fallback:  NewHashResultSource(),
		okxStream: stream,
	}
}

func (composite *CompositeResultSource) Close() {
	if composite != nil && composite.okxStream != nil {
		composite.okxStream.Close()
	}
	if composite != nil {
		_ = composite.tronHash.Close()
	}
}

func (composite CompositeResultSource) CurrentTronBlock(ctx context.Context) (int64, time.Time, error) {
	return composite.tronHash.CurrentBlock(ctx)
}

// Outcome 实现 ResultSource 接口，按 rules.Source 路由到对应的子源。
func (composite CompositeResultSource) Outcome(ctx context.Context, round game.Round, rules game.Rules) ([]string, error) {
	switch rules.Source {
	case "lulu_ws":
		return composite.lulu.Outcome(ctx, round, rules)
	case "tron_hash":
		return composite.tronHash.Outcome(ctx, round, rules)
	case "okx_kline":
		return composite.okxKline.Outcome(ctx, round, rules)
	default:
		return composite.fallback.Outcome(ctx, round, rules)
	}
}
