package config

import "testing"

func TestBackfillRequiresExplicitValidURL(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://local/test")
	t.Setenv("NATS_URL", "nats://localhost:4222")
	t.Setenv("LULU_BACKFILL_ENABLED", "false")
	t.Setenv("LULU_BACKFILL_URL", "")
	if err := Load().ValidateWorker(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LULU_BACKFILL_ENABLED", "true")
	if err := Load().ValidateWorker(); err == nil {
		t.Fatal("enabled without URL")
	}
	t.Setenv("LULU_BACKFILL_URL", "http://127.0.0.1:9082/api/v1")
	if err := Load().ValidateWorker(); err != nil {
		t.Fatal(err)
	}
}
