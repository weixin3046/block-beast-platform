package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/block-beast/platform/internal/config"
	realtimeplatform "github.com/block-beast/platform/internal/platform/realtime"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := cfg.ValidateRealtime(); err != nil {
		logger.Error("invalid realtime configuration", "error", err)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})
	hub := realtimeplatform.NewHub(cfg.AuthTokenSecret, cfg.RealtimeAllowedOrigins)
	if err := hub.ConnectNATS(cfg.NATSURL); err != nil {
		logger.Error("realtime gateway failed to connect to NATS", "error", err)
		return
	}
	defer hub.Close()
	mux.Handle("GET /v1/ws", hub)

	server := &http.Server{
		Addr:              cfg.RealtimeAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		logger.Info("realtime gateway started", "address", cfg.RealtimeAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("realtime gateway stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
