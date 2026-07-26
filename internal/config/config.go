package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment            string
	APIAddress             string
	APIAllowedOrigins      []string
	RealtimeAddress        string
	RealtimeAllowedOrigins []string
	WorkerPollInterval     time.Duration
	PostgresDSN            string
	RedisAddress           string
	NATSURL                string
	AuthTokenSecret        string
	AuthStrictPassword     bool
	AccessTokenTTL         time.Duration
	RefreshTokenTTL        time.Duration
	ChainWebhookSkew       time.Duration
	TronRPCURL             string
	OkxRESTURL             string
	PQPAAPIURL             string
	PQPAAPIKey             string
	PQPAAPISecret          string
	PQPAAssetSyncInterval  time.Duration
	WithdrawalMinMinor     int64
	WithdrawalMaxMinor     int64
	WithdrawalDailyMinor   int64
}

func Load() Config {
	environment := valueOrDefault("APP_ENV", "development")
	return Config{
		Environment:            environment,
		APIAddress:             valueOrDefault("API_ADDRESS", ":8080"),
		APIAllowedOrigins:      splitOrDefault("API_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:5173"}),
		RealtimeAddress:        valueOrDefault("REALTIME_ADDRESS", ":8081"),
		RealtimeAllowedOrigins: splitOrDefault("REALTIME_ALLOWED_ORIGINS", []string{"localhost:*", "127.0.0.1:*"}),
		WorkerPollInterval:     durationOrDefault("WORKER_POLL_INTERVAL", 5*time.Second),
		PostgresDSN:            os.Getenv("POSTGRES_DSN"),
		RedisAddress:           os.Getenv("REDIS_ADDRESS"),
		NATSURL:                os.Getenv("NATS_URL"),
		AuthTokenSecret:        os.Getenv("AUTH_TOKEN_SECRET"),
		AuthStrictPassword:     environment == "production" || boolOrDefault("AUTH_STRICT_PASSWORD_POLICY", false),
		AccessTokenTTL:         durationOrDefault("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:        durationOrDefault("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		ChainWebhookSkew:       durationOrDefault("CHAIN_WEBHOOK_ALLOWED_SKEW", 5*time.Minute),
		TronRPCURL:             os.Getenv("TRON_RPC_URL"),
		OkxRESTURL:             valueOrDefault("OKX_REST_URL", "https://www.okx.com"),
		PQPAAPIURL:             os.Getenv("PQPA_API_URL"),
		PQPAAPIKey:             os.Getenv("PQPA_API_KEY"),
		PQPAAPISecret:          os.Getenv("PQPA_API_SECRET"),
		PQPAAssetSyncInterval:  durationOrDefault("PQPA_ASSET_SYNC_INTERVAL", time.Hour),
		WithdrawalMinMinor:     int64OrDefault("WITHDRAWAL_MIN_MINOR", 1_000_000),
		WithdrawalMaxMinor:     int64OrDefault("WITHDRAWAL_MAX_MINOR", 10_000_000_000),
		WithdrawalDailyMinor:   int64OrDefault("WITHDRAWAL_DAILY_LIMIT_MINOR", 50_000_000_000),
	}
}

func int64OrDefault(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func boolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true") || value == "1"
}

func splitOrDefault(key string, fallback []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	output := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			output = append(output, trimmed)
		}
	}
	if len(output) == 0 {
		return fallback
	}
	return output
}

// 获取环境变量值，如果不存在则返回默认值
func valueOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// 时间间隔配置解析
func durationOrDefault(key string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(os.Getenv(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
