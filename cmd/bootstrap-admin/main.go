package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/application/bootstrapadmin"
	"github.com/block-beast/platform/internal/config"
	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

const passwordEnvironment = "BOOTSTRAP_ADMIN_PASSWORD"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(context.Background(), os.Args[1:]); err != nil {
		logger.Error("administrator bootstrap failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("bootstrap-admin", flag.ContinueOnError)
	loginName := flags.String("login-name", "", "administrator login name")
	displayName := flags.String("display-name", "", "administrator display name")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	password := os.Getenv(passwordEnvironment)
	_ = os.Unsetenv(passwordEnvironment)
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("%s is required", passwordEnvironment)
	}
	if strings.TrimSpace(*loginName) == "" {
		return errors.New("--login-name is required")
	}

	cfg := config.Load()
	if strings.TrimSpace(cfg.PostgresDSN) == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	connectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(connectContext, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(connectContext); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}

	userID, err := bootstrapadmin.NewService(identity.NewPostgresRepository(pool)).
		Bootstrap(connectContext, strings.TrimSpace(*loginName), strings.TrimSpace(*displayName), password)
	if err != nil {
		return err
	}
	fmt.Printf("administrator created: user_id=%s login_name=%s\n", userID, strings.TrimSpace(*loginName))
	return nil
}
