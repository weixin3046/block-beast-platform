package config

import (
	"os"
	"time"
)

type Config struct {
	Environment        string
	APIAddress         string
	RealtimeAddress    string
	WorkerPollInterval time.Duration
	PostgresDSN        string
	RedisAddress       string
	NATSURL            string
	AuthTokenSecret    string
	AccessTokenTTL     time.Duration
}

func Load() Config {
	return Config{
		Environment:        valueOrDefault("APP_ENV", "development"),
		APIAddress:         valueOrDefault("API_ADDRESS", ":8080"),
		RealtimeAddress:    valueOrDefault("REALTIME_ADDRESS", ":8081"),
		WorkerPollInterval: durationOrDefault("WORKER_POLL_INTERVAL", 5*time.Second),
		PostgresDSN:        os.Getenv("POSTGRES_DSN"),
		RedisAddress:       os.Getenv("REDIS_ADDRESS"),
		NATSURL:            os.Getenv("NATS_URL"),
		AuthTokenSecret:    os.Getenv("AUTH_TOKEN_SECRET"),
		AccessTokenTTL:     durationOrDefault("ACCESS_TOKEN_TTL", 15*time.Minute),
	}
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
