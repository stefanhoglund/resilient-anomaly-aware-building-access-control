package decisionagent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/dedupe"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/policy"
)

// Source is the value put in the Source field of envelopes this package
// publishes.
const Source = "decision-agent"

// SubscribedTypes are the event types the agent consumes.
var SubscribedTypes = []string{
	model.TypeBadgeRead,
	model.TypeOccupancyObserved,
	model.TypeDoorState,
}

// Publisher publishes an envelope to the event bus.
type Publisher interface {
	Publish(ctx context.Context, env model.Envelope) error
}

// Config configures an Agent.
type Config struct {
	// Policy is the static access policy. Defaults to
	// model.MinimalLoopPolicy().
	Policy model.PolicyConfig
	// SeenCapacity bounds the remembered badge-read ids for de-dup.
	// Default 8192.
	SeenCapacity int
	// Now supplies the current time for the events the agent emits.
	// Defaults to time.Now. Injectable for tests.
	Now    func() time.Time
	Logger *slog.Logger
}

// Agent consumes observations and emits decisions.
type Agent struct {
	cfg   model.PolicyConfig
	pub   Publisher
	log   *slog.Logger
	now   func() time.Time
	world *world
	seen  *dedupe.Set
}

// New builds an Agent. It fails if the policy configuration is invalid.
func New(cfg Config, pub Publisher) (*Agent, error) {
	pc := cfg.Policy
	if pc.Permissions == nil && pc.RoomWindows == nil && pc.RestrictedRooms == nil {
		pc = model.MinimalLoopPolicy()
	}
	if err := pc.Validate(); err != nil {
		return nil, fmt.Errorf("decisionagent: invalid policy: %w", err)
	}
	if cfg.SeenCapacity <= 0 {
		cfg.SeenCapacity = 8192
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Agent{
		cfg:   pc,
		pub:   pub,
		log:   cfg.Logger.With("component", "decision-agent"),
		now:   cfg.Now,
		world: newWorld(),
		seen:  dedupe.New(cfg.SeenCapacity),
	}, nil
}

// Handle processes one inbound event. A returned error is for logging;
// the agent keeps running regardless.
func (a *Agent) Handle(ctx context.Context, env model.Envelope) error {
	if !env.Understandable() {
		a.log.Warn("skipping event", "type", env.Type, "spec_version", env.SpecVersion, "id", env.ID)
		return nil
	}
	switch env.Type {
	case model.TypeBadgeRead:
		return a.handleBadge(ctx, env)
	case model.TypeOccupancyObserved:
		return a.handleOccupancy(env)
	case model.TypeDoorState:
		return a.handleDoorState(env)
	default:
		return nil
	}
}

func (a *Agent) handleBadge(ctx context.Context, env model.Envelope) error {
	if a.seen.SeenOrAdd(env.ID) {
		a.log.Info("duplicate badge.read ignored", "id", env.ID)
		return nil
	}
	br, err := model.Decode[model.BadgeRead](env)
	if err != nil {
		return fmt.Errorf("decisionagent: bad badge.read %s: %w", env.ID, err)
	}

	req := policy.Request{
		CardID:     br.CardID,
		DoorID:     br.DoorID,
		RoomID:     br.RoomID,
		OccurredAt: env.OccurredAt,
		Occupancy:  a.world.roomOccupancy(br.RoomID),
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("decisionagent: badge.read %s: %w", env.ID, err)
	}

	decision, command := policy.Decide(a.cfg, req)
	decision.DecisionID = model.NewID()
	command.DecisionID = decision.DecisionID
	command.CommandID = model.NewID()

	now := a.now()
	if err := a.publish(ctx, model.TypeAccessDecision, now, decision); err != nil {
		return fmt.Errorf("decisionagent: publish access.decision: %w", err)
	}
	a.log.Info("decision",
		"decision_id", decision.DecisionID,
		"card", br.CardID, "room", br.RoomID, "door", br.DoorID,
		"action", decision.Action, "rule", decision.Evidence.PolicyRule,
		"trigger_id", env.ID)

	// Only command the door when the lock actually needs to change. An
	// alert whose door is already locked still produced the decision
	// above; it just needs no actuation.
	lastLock := a.world.doorLock(command.DoorID)
	if command.DesiredLock == lastLock {
		a.log.Info("door already in desired lock; no command issued",
			"door", command.DoorID, "lock", lastLock, "decision_id", decision.DecisionID)
		return nil
	}

	if err := a.publish(ctx, model.TypeDoorCommand, now, command); err != nil {
		return fmt.Errorf("decisionagent: publish door.command: %w", err)
	}
	a.log.Info("issued door.command",
		"command_id", command.CommandID, "decision_id", decision.DecisionID,
		"door", command.DoorID, "desired_lock", command.DesiredLock, "ttl_seconds", command.TTLSeconds)
	return nil
}

func (a *Agent) handleOccupancy(env model.Envelope) error {
	obs, err := model.Decode[model.OccupancyObserved](env)
	if err != nil {
		return fmt.Errorf("decisionagent: bad occupancy.observed %s: %w", env.ID, err)
	}
	if a.world.applyOccupancy(obs, env.OccurredAt) {
		a.log.Info("occupancy updated",
			"room", obs.RoomID, "present", obs.Present, "count", obs.OccupantCount,
			"occurred_at", env.OccurredAt)
	} else {
		a.log.Warn("stale occupancy observation ignored",
			"room", obs.RoomID, "occurred_at", env.OccurredAt)
	}
	return nil
}

func (a *Agent) handleDoorState(env model.Envelope) error {
	ds, err := model.Decode[model.DoorState](env)
	if err != nil {
		return fmt.Errorf("decisionagent: bad door.state %s: %w", env.ID, err)
	}
	if a.world.applyDoorState(ds) {
		a.log.Info("door state updated",
			"door", ds.DoorID, "lock", ds.Lock, "contact", ds.Contact,
			"version", ds.BuildSimVersion, "applied", ds.Applied)
	} else {
		a.log.Warn("stale door.state ignored",
			"door", ds.DoorID, "version", ds.BuildSimVersion)
	}
	return nil
}

func (a *Agent) publish(ctx context.Context, typ string, occurredAt time.Time, payload any) error {
	env, err := model.NewEnvelope(Source, typ, occurredAt, payload)
	if err != nil {
		return err
	}
	return a.pub.Publish(ctx, env)
}
