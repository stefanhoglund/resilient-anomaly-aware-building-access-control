// Command eventbus is the phase-1 message transport: an in-memory
// publish/subscribe relay over HTTP (ADR 0003).
//
//	POST /publish     accept one event envelope
//	GET  /subscribe   Server-Sent-Events stream (?types= filter,
//	                  Last-Event-ID resume)
//	GET  /healthz     status and counters
//
// Configuration (environment):
//
//	EVENTBUS_ADDR      listen address, default ":8080"
//	EVENTBUS_BUFFER    per-subscriber queue depth, default 256
//	EVENTBUS_HISTORY   retained events for resume, default 1024
//	EVENTBUS_HEARTBEAT SSE keep-alive interval, default 15s
//
//	FAULT_DROP_RATE FAULT_DUPLICATE FAULT_DELAY_MS FAULT_REORDER FAULT_SEED
//	                  message-fault injection for the resilience tests;
//	                  off unless set, logged loudly when on.
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

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/eventbus"
)

const defaultAddr = ":8080"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, logger))
}

func run(ctx context.Context, logger *slog.Logger) int {
	addr := os.Getenv("EVENTBUS_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	cfg, err := eventbus.BrokerConfigFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		return 1
	}
	cfg.Logger = logger

	broker := eventbus.NewBroker(cfg)

	srv := &http.Server{
		Addr:              addr,
		Handler:           broker,
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: /subscribe streams indefinitely.
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("event bus listening", "addr", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			return 1
		}
		return 0
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		return 1
	}
	logger.Info("event bus stopped")
	return 0
}
