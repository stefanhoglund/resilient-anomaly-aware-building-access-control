// Command door-actuator applies access decisions to a physical door.
//
// It subscribes to door.command on the event bus, sets the door
// equipment's lock actuator in BuildSim, and publishes door.state after
// every attempt (including failures). In phase 1 (ADR 0003) it is the
// only process that writes BuildSim.
//
// Configuration (environment):
//
//	BUILDSIM_URL       required — BuildSim origin
//	BUILDSIM_TIMEOUT   per-call timeout, default 5s
//	EVENTBUS_URL       event bus origin, default http://127.0.0.1:8080
//	DOOR_ID            the door to serve, default "door-1"
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/dooractuator"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/eventbus"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, logger))
}

func run(ctx context.Context, logger *slog.Logger) int {
	if err := model.Validate(); err != nil {
		logger.Error("model mapping invalid", "err", err)
		return 1
	}

	doorID := os.Getenv("DOOR_ID")
	if doorID == "" {
		doorID = model.Door1
	}

	bsCfg, err := buildsim.ConfigFromEnv()
	if err != nil {
		logger.Error("BuildSim configuration invalid", "err", err)
		return 1
	}
	bs, err := buildsim.New(bsCfg)
	if err != nil {
		logger.Error("BuildSim client", "err", err)
		return 1
	}

	busCfg, err := eventbus.ConfigFromEnv()
	if err != nil {
		logger.Error("event bus configuration invalid", "err", err)
		return 1
	}
	bus, err := eventbus.New(busCfg)
	if err != nil {
		logger.Error("event bus client", "err", err)
		return 1
	}

	act, err := dooractuator.New(dooractuator.Config{DoorID: doorID, Logger: logger}, bs, bus)
	if err != nil {
		logger.Error("actuator", "err", err)
		return 1
	}

	// Best-effort warm-up: register the door equipment now so the first
	// command is fast and BuildSim shows the door immediately. A failure
	// here is not fatal — Handle retries on the first command.
	if err := act.EnsureReady(ctx); err != nil {
		logger.Warn("could not pre-register door equipment; will retry on first command", "err", err)
	}

	sub, err := bus.Subscribe(ctx, eventbus.SubscribeOptions{Types: []string{model.TypeDoorCommand}})
	if err != nil {
		logger.Error("subscribe to door.command", "err", err)
		return 1
	}
	defer sub.Close()

	logger.Info("door-actuator running", "door", doorID, "buildsim", bsCfg.BaseURL, "bus", busCfg.BaseURL)

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return 0
		case env, ok := <-sub.C:
			if !ok {
				if err := sub.Err(); err != nil {
					logger.Error("subscription ended", "err", err)
					return 1
				}
				return 0
			}
			handle(ctx, logger, act, env)
		}
	}
}

func handle(ctx context.Context, logger *slog.Logger, act *dooractuator.Actuator, env model.Envelope) {
	if !env.Understandable() {
		logger.Warn("skipping event", "type", env.Type, "spec_version", env.SpecVersion, "id", env.ID)
		return
	}
	cmd, err := model.Decode[model.DoorCommand](env)
	if err != nil {
		logger.Error("bad door.command payload", "id", env.ID, "err", err)
		return
	}
	if _, err := act.Handle(ctx, cmd); err != nil {
		logger.Error("handling door.command", "id", env.ID, "command_id", cmd.CommandID, "err", err)
	}
}
