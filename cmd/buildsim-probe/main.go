// Command buildsim-probe is the first vertical slice: it proves a Go
// process can be configured for, reach, and correctly parse BuildSim.
//
// It loads configuration from the environment, calls /healthz and
// /api/building once each, logs each outcome as a structured event, and
// exits 0 on success or 1 on any failure. It is a short-lived diagnostic,
// not a long-running service.
//
// Configuration:
//
//	BUILDSIM_URL      required, e.g. http://127.0.0.1:9090
//	BUILDSIM_TIMEOUT  optional Go duration, default 5s
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Graceful shutdown: a SIGINT/SIGTERM cancels ctx so an in-flight
	// request is abandoned cleanly instead of blocking to its timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, logger))
}

// run holds all the logic so it is testable without spawning a process.
// It returns the intended process exit code.
func run(ctx context.Context, logger *slog.Logger) int {
	cfg, err := buildsim.ConfigFromEnv()
	if err != nil {
		logger.Error("configuration invalid", "err", err)
		return 1
	}
	logger.Info("configuration loaded",
		"buildsim_url", cfg.BaseURL, "timeout", cfg.Timeout.String())

	client, err := buildsim.New(cfg)
	if err != nil {
		logger.Error("client construction failed", "err", err)
		return 1
	}

	health, err := client.Health(ctx)
	if err != nil {
		logger.Error("healthz call failed", "err", err)
		return 1
	}
	if !health.OK() {
		logger.Error("buildsim reports unhealthy",
			"status", health.Status, "err", buildsim.ErrUnhealthy)
		return 1
	}
	logger.Info("healthz ok", "status", health.Status)

	building, err := client.Building(ctx)
	if err != nil {
		logger.Error("building call failed", "err", err)
		return 1
	}
	logger.Info("building fetched",
		"name", building.Name,
		"levels", len(building.Levels),
		"level_ids", levelIDs(building))

	logger.Info("probe succeeded")
	return 0
}

func levelIDs(b buildsim.Building) string {
	if len(b.Levels) == 0 {
		return ""
	}
	out := b.Levels[0].ID
	for _, l := range b.Levels[1:] {
		out += "," + l.ID
	}
	return fmt.Sprintf("[%s]", out)
}
