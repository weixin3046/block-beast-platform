package main

import (
	"context"
	"time"
)

// 采集事件持久化到 inbox 后才继续；数据库重试由独立消费者执行。
// 发布确认丢失时重试沿用消息 ID，消费入库仍由幂等事务兜底。
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

// Give already received events a bounded persistence window on reload/shutdown.
func luluPublishContext(parent context.Context, grace time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-parent.Done():
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			cancel()
		}
	}()
	return ctx, cancel
}
