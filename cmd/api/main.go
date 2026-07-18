package main

import (
	"context"   // 上下文，用于超时控制、服务关闭
	"errors"    // 错误判断工具
	"log/slog"  // Go1.21+ 标准结构化日志库（JSON日志）
	"net/http"  // 标准http服务
	"os"        // 系统操作、进程退出
	"os/signal" // 操作系统信号捕获
	"syscall"   // 系统信号常量（SIGINT、SIGTERM）
	"time"      // 时间、超时设置

	"github.com/block-beast/platform/internal/config"           // 配置加载
	"github.com/block-beast/platform/internal/platform/httpapi" // API路由/业务处理器
)

func main() {
	cfg := config.Load()                                                                      // 加载配置文件
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))                                   // 创建JSON日志记录器
	server := &http.Server{Addr: cfg.APIAddress, Handler: httpapi.New(cfg, logger).Handler()} // 创建HTTP服务器实例

	go func() {
		logger.Info("api started", "address", cfg.APIAddress, "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	shutdown := make(chan os.Signal, 1)                                      // 创建一个通道，用于接收系统信号
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)                 // 监听SIGINT和SIGTERM信号
	<-shutdown                                                               // 阻塞等待信号到来
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // 创建一个带超时的上下文，超时时间为10秒
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
