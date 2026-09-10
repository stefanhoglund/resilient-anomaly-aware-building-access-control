// Command decision-agent is the autonomous decision service.
//
// It subscribes to badge.read, occupancy.observed and door.state on the
// event bus, maintains a small world-state view, and on every badge read
// runs the deterministic baseline policy to publish an access.decision
// and, when the lock must change, a door.command (ADR 0003).
//
// It does not talk to BuildSim — the door-actuator does that.
//
// Configuration (environment):
//
//	EVENTBUS_URL       event bus origin, default http://127.0.0.1:8080
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/decisionagent"
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

	agent, err := decisionagent.New(decisionagent.Config{Logger: logger}, bus)
	if err != nil {
		logger.Error("decision agent", "err", err)
		return 1
	}

	sub, err := bus.Subscribe(ctx, eventbus.SubscribeOptions{Types: decisionagent.SubscribedTypes})
	if err != nil {
		logger.Error("subscribe", "err", err)
		return 1
	}
	defer sub.Close()

	logger.Info("decision-agent running", "bus", busCfg.BaseURL, "types", decisionagent.SubscribedTypes)

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
			if err := agent.Handle(ctx, env); err != nil {
				logger.Error("handling event", "id", env.ID, "type", env.Type, "err", err)
			}
		}
	}
}
