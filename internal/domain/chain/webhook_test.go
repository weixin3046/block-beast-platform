package chain

import (
	"errors"
	"testing"
	"time"
)

func TestVerifyWebhook(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"event":"recharge","id":42}`)
	timestamp := "1784376000000"
	signature := SignWebhook("test-secret", "POST", "/v1/chain/callback", timestamp, "nonce-1", body)
	if err := VerifyWebhook("test-secret", "POST", "/v1/chain/callback", timestamp, "nonce-1", body, signature, now, 5*time.Minute); err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
}

func TestVerifyWebhookRejectsTamperingAndExpiredRequest(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"event":"recharge","id":42}`)
	timestamp := "1784376000000"
	signature := SignWebhook("test-secret", "POST", "/v1/chain/callback", timestamp, "nonce-1", body)
	if err := VerifyWebhook("test-secret", "POST", "/v1/chain/callback", timestamp, "nonce-1", []byte(`{"event":"recharge","id":43}`), signature, now, 5*time.Minute); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want invalid signature", err)
	}
	if err := VerifyWebhook("test-secret", "POST", "/v1/chain/callback", "1784375400000", "nonce-1", body, signature, now, 5*time.Minute); !errors.Is(err, ErrTimestampOutOfRange) {
		t.Fatalf("error = %v, want timestamp out of range", err)
	}
}
