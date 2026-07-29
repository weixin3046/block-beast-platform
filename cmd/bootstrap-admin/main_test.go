package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresPasswordEnvironment(t *testing.T) {
	t.Setenv(passwordEnvironment, "")
	err := run(context.Background(), []string{"--login-name", "first-admin"})
	if err == nil || !strings.Contains(err.Error(), passwordEnvironment+" is required") {
		t.Fatalf("run error = %v", err)
	}
}

func TestRunRequiresLoginName(t *testing.T) {
	t.Setenv(passwordEnvironment, "valid-password-12")
	err := run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "--login-name is required") {
		t.Fatalf("run error = %v", err)
	}
}
