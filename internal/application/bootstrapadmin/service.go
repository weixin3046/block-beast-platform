package bootstrapadmin

import (
	"context"
	"errors"
	"regexp"

	"github.com/block-beast/platform/internal/domain/identity"
)

var ErrInvalidLoginName = errors.New("login name must be 3-32 chars of letters, digits, '-' or '_'")

var loginNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type AdminCreator interface {
	CreateFirstAdmin(ctx context.Context, loginName string, displayName string, passwordHash string) (string, error)
}

type Service struct {
	creator AdminCreator
}

func NewService(creator AdminCreator) *Service {
	return &Service{creator: creator}
}

func (service *Service) Bootstrap(ctx context.Context, loginName string, displayName string, password string) (string, error) {
	if !loginNamePattern.MatchString(loginName) {
		return "", ErrInvalidLoginName
	}
	if displayName == "" {
		displayName = loginName
	}
	passwordHash, err := identity.HashPassword(password)
	if err != nil {
		return "", err
	}
	return service.creator.CreateFirstAdmin(ctx, loginName, displayName, passwordHash)
}
