package bootstrapadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/block-beast/platform/internal/domain/identity"
)

type stubCreator struct {
	loginName    string
	displayName  string
	passwordHash string
	userID       string
	err          error
}

func (creator *stubCreator) CreateFirstAdmin(_ context.Context, loginName string, displayName string, passwordHash string) (string, error) {
	creator.loginName = loginName
	creator.displayName = displayName
	creator.passwordHash = passwordHash
	return creator.userID, creator.err
}

func TestBootstrapValidatesInput(t *testing.T) {
	service := NewService(&stubCreator{})
	if _, err := service.Bootstrap(context.Background(), "ab", "", "valid-password-12"); !errors.Is(err, ErrInvalidLoginName) {
		t.Fatalf("invalid login name error = %v", err)
	}
}

func TestBootstrapHashesPasswordAndDefaultsDisplayName(t *testing.T) {
	creator := &stubCreator{userID: "admin-id"}
	service := NewService(creator)

	userID, err := service.Bootstrap(context.Background(), "first-admin", "", "short")
	if err != nil {
		t.Fatal(err)
	}
	if userID != "admin-id" || creator.loginName != "first-admin" || creator.displayName != "first-admin" {
		t.Fatalf("created admin = %q, %q, %q", userID, creator.loginName, creator.displayName)
	}
	if creator.passwordHash == "short" || !identity.VerifyPassword(creator.passwordHash, "short") {
		t.Fatal("password was not hashed with the identity password hasher")
	}
}

func TestBootstrapPropagatesCreatorError(t *testing.T) {
	expected := errors.New("create failed")
	service := NewService(&stubCreator{err: expected})
	if _, err := service.Bootstrap(context.Background(), "first-admin", "", "valid-password-12"); !errors.Is(err, expected) {
		t.Fatalf("bootstrap error = %v", err)
	}
}
