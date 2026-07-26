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
