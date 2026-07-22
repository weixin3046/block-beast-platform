package credit

import (
	"context"
	"errors"
	"testing"
)

func TestAdminCreditValidatesInput(t *testing.T) {
	service := NewService(nil)
	ctx := context.Background()

	// 金额必须为正。
	if _, err := service.AdminCredit(ctx, AdminCreditInput{UserID: "u1", Currency: CurrencyPoints, AmountMinor: 0, RequestID: "r1"}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero amount error = %v, want ErrInvalidAmount", err)
	}
	if _, err := service.AdminCredit(ctx, AdminCreditInput{UserID: "u1", Currency: CurrencyPoints, AmountMinor: -100, RequestID: "r1"}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative amount error = %v, want ErrInvalidAmount", err)
	}

	// 币种必须是三种之一。
	if _, err := service.AdminCredit(ctx, AdminCreditInput{UserID: "u1", Currency: "BTC", AmountMinor: 100, RequestID: "r1"}); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("invalid currency error = %v, want ErrInvalidCurrency", err)
	}

	// user_id 和 request_id 必填。
	if _, err := service.AdminCredit(ctx, AdminCreditInput{Currency: CurrencyPoints, AmountMinor: 100, RequestID: "r1"}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("empty user error = %v, want ErrUserNotFound", err)
	}
	if _, err := service.AdminCredit(ctx, AdminCreditInput{UserID: "u1", Currency: CurrencyPoints, AmountMinor: 100}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("empty request_id error = %v, want ErrUserNotFound", err)
	}
}

func TestConsumeStaminaValidatesInput(t *testing.T) {
	service := NewService(nil)
	ctx := context.Background()

	// 金额必须为正。
	if _, err := service.ConsumeStamina(ctx, ConsumeStaminaInput{UserID: "u1", AmountMinor: 0, ActivityID: "a1"}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero amount error = %v, want ErrInvalidAmount", err)
	}

	// user_id 和 activity_id 必填。
	if _, err := service.ConsumeStamina(ctx, ConsumeStaminaInput{AmountMinor: 10, ActivityID: "a1"}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("empty user error = %v, want ErrUserNotFound", err)
	}
	if _, err := service.ConsumeStamina(ctx, ConsumeStaminaInput{UserID: "u1", AmountMinor: 10}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("empty activity error = %v, want ErrUserNotFound", err)
	}
}
