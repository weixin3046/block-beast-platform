package config

import (
	"errors"
	"testing"
)

func TestValidateAPIRejectsMissingAuthenticationOutsideDevelopment(t *testing.T) {
	config := Config{Environment: "production", PostgresDSN: "postgres://database"}
	if err := config.ValidateAPI(); !errors.Is(err, ErrInsecureAuthentication) {
		t.Fatalf("error = %v, want ErrInsecureAuthentication", err)
	}
	config.Environment = "development"
	if err := config.ValidateAPI(); err != nil {
		t.Fatalf("development API configuration: %v", err)
	}
}

func TestValidateRealtimeAlwaysRequiresAuthentication(t *testing.T) {
	config := Config{Environment: "development", NATSURL: "nats://nats:4222"}
	if err := config.ValidateRealtime(); !errors.Is(err, ErrInsecureAuthentication) {
		t.Fatalf("error = %v, want ErrInsecureAuthentication", err)
	}
	config.AuthTokenSecret = "0123456789abcdef0123456789abcdef"
	config.PostgresDSN = "postgres://database"
	if err := config.ValidateRealtime(); err != nil {
		t.Fatalf("realtime configuration: %v", err)
	}
}

func TestProcessConfigurationRequiresDependencies(t *testing.T) {
	if err := (Config{}).ValidateAPI(); err == nil {
		t.Fatal("API must require PostgreSQL")
	}
	if err := (Config{AuthTokenSecret: "0123456789abcdef0123456789abcdef"}).ValidateRealtime(); err == nil {
		t.Fatal("realtime must require NATS")
	}
	if err := (Config{PostgresDSN: "postgres://database"}).ValidateWorker(); err == nil {
		t.Fatal("worker must require NATS")
	}
}
