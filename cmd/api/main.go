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

	"github.com/block-beast/platform/internal/application/agent"
	"github.com/block-beast/platform/internal/application/audit"   // 审计应用服务
	"github.com/block-beast/platform/internal/application/auth"    // 登录认证应用服务
	"github.com/block-beast/platform/internal/application/betting" // 下注应用服务
	"github.com/block-beast/platform/internal/application/chain"   // 链上充提应用服务
	"github.com/block-beast/platform/internal/application/chat"
	"github.com/block-beast/platform/internal/application/credit" // 积分/体力充值应用服务
	"github.com/block-beast/platform/internal/application/operations"
	"github.com/block-beast/platform/internal/application/pqpaassets"
	"github.com/block-beast/platform/internal/application/settlement" // 结算应用服务
	"github.com/block-beast/platform/internal/application/task"       // 任务/签到应用服务
	"github.com/block-beast/platform/internal/config"                 // 配置加载
	"github.com/block-beast/platform/internal/domain/game"            // 游戏轮次仓储
	"github.com/block-beast/platform/internal/domain/identity"        // 身份认证仓储
	"github.com/block-beast/platform/internal/domain/wallet"          // 钱包仓储
	"github.com/block-beast/platform/internal/platform/httpapi"       // API路由/业务处理器
	"github.com/block-beast/platform/internal/platform/pqpa"
	"github.com/jackc/pgx/v5/pgxpool" // PostgreSQL连接池
)

func main() {
	cfg := config.Load()                                    // 加载配置文件
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)) // 创建JSON日志记录器
	if err := cfg.ValidateAPI(); err != nil {
		logger.Error("invalid API configuration", "error", err)
		return
	}
	pool, err := pgxpool.New(context.Background(), cfg.PostgresDSN)
	if err != nil {
		logger.Error("api failed to connect to PostgreSQL", "error", err)
		return
	}
	defer pool.Close()
	creditService := credit.NewService(pool)
	taskService := task.NewService(pool, creditService)
	bettingService := betting.NewService(pool).WithTaskHook(taskService)
	cancellationService := settlement.NewService(pool)
	options := []httpapi.Option{httpapi.WithAudit(audit.NewService(pool))}
	if cfg.AuthTokenSecret == "" {
		logger.Warn("AUTH_TOKEN_SECRET is not set; business endpoints are unauthenticated")
	} else {
		identityRepository := identity.NewPostgresRepository(pool)
		loginPolicy := auth.LoginProtectionPolicy{
			MaxFailures: cfg.LoginMaxFailures,
			Window:      cfg.LoginFailureWindow,
			Lockout:     cfg.LoginLockoutDuration,
		}
		if err := loginPolicy.Validate(); err != nil {
			logger.Error("invalid login protection policy", "error", err)
			return
		}
		authService := auth.NewService(identityRepository, cfg.AuthTokenSecret, cfg.AccessTokenTTL).
			WithStrictPasswordPolicy(cfg.AuthStrictPassword).
			WithRegistrar(identityRepository).
			WithSessions(identityRepository, cfg.RefreshTokenTTL).
			WithLoginProtection(identityRepository, loginPolicy)
		options = append(options, httpapi.WithAuth(httpapi.NewAuthenticator(cfg.AuthTokenSecret)), httpapi.WithLogin(authService), httpapi.WithRegister(authService), httpapi.WithSessions(authService))
	}
	withdrawalPolicy := chain.WithdrawalPolicy{
		MinimumMinor:    cfg.WithdrawalMinMinor,
		MaximumMinor:    cfg.WithdrawalMaxMinor,
		DailyLimitMinor: cfg.WithdrawalDailyMinor,
	}
	if err := withdrawalPolicy.Validate(); err != nil {
		logger.Error("invalid withdrawal policy", "error", err)
		return
	}
	chainService := chain.NewService(pool).WithWithdrawalPolicy(withdrawalPolicy)
	if cfg.PQPAAPIURL != "" && cfg.PQPAAPIKey != "" && cfg.PQPAAPISecret != "" {
		pqpaClient := pqpa.NewClient(cfg.PQPAAPIURL, cfg.PQPAAPIKey, cfg.PQPAAPISecret, nil)
		chainService.WithDepositAddressProvider(pqpaClient)
		options = append(options, httpapi.WithProviderAssets(pqpaassets.NewService(pool, pqpa.AssetProvider{Client: pqpaClient})))
	} else {
		logger.Warn("PQPA API configuration is incomplete; deposit address creation is disabled")
	}
	options = append(options, httpapi.WithWithdrawals(chainService))
	options = append(options, httpapi.WithChat(chat.NewService(pool)))
	options = append(options, httpapi.WithDepositHistory(chainService))
	options = append(options, httpapi.WithAgents(agent.NewService(pool)))
	options = append(options, httpapi.WithDepositAddresses(chainService))
	options = append(options, httpapi.WithCredits(creditService), httpapi.WithTasks(taskService))
	operationsService := operations.NewService(pool)
	options = append(options, httpapi.WithUserAdmin(operationsService), httpapi.WithOperations(operationsService), httpapi.WithGameAdmin(operationsService))
	if cfg.PQPAAPISecret == "" {
		logger.Warn("PQPA_API_SECRET is not set; chain deposit webhook is disabled")
	} else {
		options = append(options, httpapi.WithChainDeposits(cfg.PQPAAPIKey, cfg.PQPAAPISecret, cfg.ChainWebhookSkew, chainService), httpapi.WithChainWithdrawalStatuses(chainService))
	}
	server := &http.Server{
		Addr:              cfg.APIAddress,
		Handler:           httpapi.New(cfg, logger, bettingService, pool, wallet.NewPostgresRepository(pool), game.NewPostgresRepository(pool), bettingService, cancellationService, options...).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

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
