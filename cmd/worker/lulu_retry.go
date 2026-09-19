package main

import (
	"context"
	"time"
)

// 同一事件提交成功后才处理后续事件，避免暂时的数据库故障丢失开奖结果。
// Handle 使用幂等事务；即使提交成功但响应丢失，重试也不会重复派彩。
func retryLuluEvent(ctx context.Context, interval time.Duration, apply func() error, report func(error)) bool {
	for ctx.Err() == nil {
		if err := apply(); err == nil {
			return true
		} else if ctx.Err() == nil {
			report(err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return false
}
