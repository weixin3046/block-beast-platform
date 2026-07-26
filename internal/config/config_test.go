package config

import "testing"

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
