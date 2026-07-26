package config

import (
	"testing"
	"time"
)

func TestPasswordPolicyIsForcedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("AUTH_STRICT_PASSWORD_POLICY", "false")
	if Load().AuthStrictPassword {
		t.Fatal("development password policy should be disabled")
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_STRICT_PASSWORD_POLICY", "false")
	if !Load().AuthStrictPassword {
		t.Fatal("production must force strict password policy")
	}
}

func TestSensitiveRPCAndWithdrawalRiskConfiguration(t *testing.T) {
	t.Setenv("TRON_RPC_URL", "")
	t.Setenv("QUICKNODE_TRON_URL", "")
	t.Setenv("WITHDRAWAL_MIN_MINOR", "2000000")
	t.Setenv("WITHDRAWAL_MAX_MINOR", "9000000")
	t.Setenv("WITHDRAWAL_DAILY_LIMIT_MINOR", "15000000")
	config := Load()
	if config.TronRPCURL != "" {
		t.Fatal("TRON RPC URL must not have a credential-bearing default")
	}
	if config.WithdrawalMinMinor != 2_000_000 || config.WithdrawalMaxMinor != 9_000_000 || config.WithdrawalDailyMinor != 15_000_000 {
		t.Fatalf("withdrawal limits = %d/%d/%d", config.WithdrawalMinMinor, config.WithdrawalMaxMinor, config.WithdrawalDailyMinor)
	}
}

func TestQuickNodeURLTakesPrecedenceOverLegacyName(t *testing.T) {
	t.Setenv("TRON_RPC_URL", "https://legacy.invalid/jsonrpc")
	t.Setenv("QUICKNODE_TRON_URL", "https://quicknode.invalid/jsonrpc")
	if got := Load().TronRPCURL; got != "https://quicknode.invalid/jsonrpc" {
		t.Fatalf("TRON RPC URL = %q", got)
	}
}

func TestLoginProtectionConfiguration(t *testing.T) {
	t.Setenv("LOGIN_MAX_FAILURES", "7")
	t.Setenv("LOGIN_FAILURE_WINDOW", "20m")
	t.Setenv("LOGIN_LOCKOUT_DURATION", "30m")
	config := Load()
	if config.LoginMaxFailures != 7 || config.LoginFailureWindow != 20*time.Minute || config.LoginLockoutDuration != 30*time.Minute {
		t.Fatalf("login protection = %d/%s/%s", config.LoginMaxFailures, config.LoginFailureWindow, config.LoginLockoutDuration)
	}

	t.Setenv("LOGIN_MAX_FAILURES", "0")
	t.Setenv("LOGIN_FAILURE_WINDOW", "invalid")
	t.Setenv("LOGIN_LOCKOUT_DURATION", "-1m")
	config = Load()
	if config.LoginMaxFailures != 5 || config.LoginFailureWindow != 15*time.Minute || config.LoginLockoutDuration != 15*time.Minute {
		t.Fatalf("invalid login protection fallback = %d/%s/%s", config.LoginMaxFailures, config.LoginFailureWindow, config.LoginLockoutDuration)
	}
}
