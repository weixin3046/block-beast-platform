package config

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInsecureAuthentication = errors.New("secure authentication configuration is required")

func (config Config) ValidateAPI() error {
	if strings.TrimSpace(config.PostgresDSN) == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	if config.Environment != "development" && len(config.AuthTokenSecret) < 32 {
		return fmt.Errorf("%w: AUTH_TOKEN_SECRET must contain at least 32 bytes outside development", ErrInsecureAuthentication)
	}
	return nil
}

func (config Config) ValidateRealtime() error {
	if len(config.AuthTokenSecret) < 32 {
		return fmt.Errorf("%w: AUTH_TOKEN_SECRET must contain at least 32 bytes", ErrInsecureAuthentication)
	}
	if strings.TrimSpace(config.NATSURL) == "" {
		return errors.New("NATS_URL is required")
	}
	if strings.TrimSpace(config.PostgresDSN) == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	return nil
}

func (config Config) ValidateWorker() error {
	if strings.TrimSpace(config.PostgresDSN) == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	if strings.TrimSpace(config.NATSURL) == "" {
		return errors.New("NATS_URL is required")
	}
	return nil
}
