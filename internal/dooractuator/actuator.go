package dooractuator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// Source is the value put in DoorState.Source for states this package emits.
const Source = "actuator"

// Equipment sensor and actuator ids on a door.
const (
	sensorContact    = "contact"
	sensorBadge      = "badge"
	sensorBadgePulse = "badge_pulse"
	actuatorLock     = "lock"
)

// BuildSim is the slice of the BuildSim client this package needs.
type BuildSim interface {
	EnsureEquipment(ctx context.Context, eq buildsim.Equipment) (buildsim.Equipment, error)
	PutEquipment(ctx context.Context, eq buildsim.Equipment) (buildsim.Equipment, error)
}

// Publisher publishes an envelope to the event bus.
type Publisher interface {
	Publish(ctx context.Context, env model.Envelope) error
}

// Config configures an Actuator.
type Config struct {
	// DoorID is the single door this actuator serves.
	DoorID string
	// Retries is how many extra attempts a BuildSim write gets after the
	// first fails. Default 3.
	Retries int
	// RetryWait is the base backoff between attempts (multiplied by the
	// attempt number). Default 200ms.
	RetryWait time.Duration
	// SeenCapacity bounds the remembered command-id set for de-dup.
	// Default 4096.
	SeenCapacity int
	Logger       *slog.Logger
}

// Actuator applies commands for one door.
type Actuator struct {
	doorID    string
	equip     model.BuildSimRoom
	bs        BuildSim
	pub       Publisher
	log       *slog.Logger
	retries   int
	retryWait time.Duration

	current buildsim.Equipment
	ready   bool
	seen    *seenSet
}

// New builds an Actuator. It fails if DoorID is not in the model mapping.
func New(cfg Config, bs BuildSim, pub Publisher) (*Actuator, error) {
	m, ok := model.LookupDoor(cfg.DoorID)
	if !ok {
		return nil, fmt.Errorf("dooractuator: unknown door %q", cfg.DoorID)
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
	if cfg.RetryWait <= 0 {
		cfg.RetryWait = 200 * time.Millisecond
	}
	if cfg.SeenCapacity <= 0 {
		cfg.SeenCapacity = 4096
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Actuator{
		doorID:    cfg.DoorID,
		equip:     m.Equipment,
		bs:        bs,
		pub:       pub,
		log:       cfg.Logger.With("component", "door-actuator", "door", cfg.DoorID),
		retries:   cfg.Retries,
		retryWait: cfg.RetryWait,
		seen:      newSeenSet(cfg.SeenCapacity),
	}, nil
}

// blank is the door equipment as this actuator first creates it: locked,
// with the three sensors the door carries at their zero values.
func (a *Actuator) blank() buildsim.Equipment {
	return buildsim.Equipment{
		ID:       a.doorID,
		Name:     "Door " + a.doorID,
		Type:     "door",
		Category: "access",
		Level:    a.equip.Level,
		Room:     a.equip.Name,
		Sensors: []buildsim.Sensor{
			{ID: sensorContact, Name: "Contact", DataType: buildsim.DataTypeBinary},
			{ID: sensorBadge, Name: "Last Badge", DataType: buildsim.DataTypeText},
			{ID: sensorBadgePulse, Name: "Badge Pulse", DataType: buildsim.DataTypeBinary},
		},
		Actuators: []buildsim.Actuator{
			{ID: actuatorLock, Name: "Lock", State: string(model.LockLocked)},
		},
	}
}

// EnsureReady registers the door equipment with BuildSim if it is not
// there yet and loads its current state. Safe to call more than once. If
// it is not called, Handle calls it on the first command.
func (a *Actuator) EnsureReady(ctx context.Context) error {
	if a.ready {
		return nil
	}
	eq, err := a.bs.EnsureEquipment(ctx, a.blank())
	if err != nil {
		return fmt.Errorf("dooractuator: ensure door equipment: %w", err)
	}
	a.current = eq
	a.ready = true
	a.log.Info("door equipment ready",
		"level", eq.Level, "room", eq.Room, "version", eq.Version, "lock", a.currentLock())
	return nil
}

// Handle applies one command and publishes the resulting DoorState. The
// returned DoorState is the one published; a returned error means the
// state could not be published (the state itself still reports the
// actuation outcome via Applied/Error).
func (a *Actuator) Handle(ctx context.Context, cmd model.DoorCommand) (model.DoorState, error) {
	if cmd.DoorID != a.doorID {
		st := a.stateWithError(fmt.Errorf("command for door %q, this actuator serves %q", cmd.DoorID, a.doorID))
		return a.emit(ctx, st)
	}

	if !a.ready {
		if err := a.EnsureReady(ctx); err != nil {
			return a.emit(ctx, a.stateWithError(err))
		}
	}

	if a.seen.has(cmd.CommandID) {
		a.log.Info("duplicate command ignored", "command_id", cmd.CommandID, "decision_id", cmd.DecisionID)
		return a.emit(ctx, a.state(true, nil))
	}
	a.seen.add(cmd.CommandID)

	if cmd.TTLSeconds > 0 {
		a.log.Info("command has a TTL; relock-on-expiry is the decision service's job, not enforced here",
			"command_id", cmd.CommandID, "ttl_seconds", cmd.TTLSeconds)
	}

	if a.currentLock() == cmd.DesiredLock {
		a.log.Info("door already in desired state", "lock", cmd.DesiredLock, "command_id", cmd.CommandID)
		return a.emit(ctx, a.state(true, nil))
	}

	updated, err := a.applyLock(ctx, cmd.DesiredLock)
	if err != nil {
		a.log.Error("could not apply command", "command_id", cmd.CommandID, "err", err)
		return a.emit(ctx, a.stateWithError(err))
	}
	a.current = updated
	a.log.Info("door lock changed",
		"lock", a.currentLock(), "version", updated.Version,
		"command_id", cmd.CommandID, "decision_id", cmd.DecisionID)
	return a.emit(ctx, a.state(true, nil))
}

// applyLock sets the lock actuator to lock and PUTs the whole equipment,
// retrying with backoff. If BuildSim has forgotten the equipment (404),
// it is recreated and the attempt is retried.
func (a *Actuator) applyLock(ctx context.Context, lock model.LockState) (buildsim.Equipment, error) {
	want := a.current
	want.Actuators = cloneActuators(a.current.Actuators)
	want.Sensors = cloneSensors(a.current.Sensors)
	setActuatorState(&want, actuatorLock, string(lock))

	var lastErr error
	for attempt := 0; attempt <= a.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(a.retryWait * time.Duration(attempt)):
			case <-ctx.Done():
				return buildsim.Equipment{}, ctx.Err()
			}
		}

		updated, err := a.bs.PutEquipment(ctx, want)
		if err == nil {
			return updated, nil
		}
		lastErr = err

		if errors.Is(err, buildsim.ErrEquipmentNotFound) {
			a.log.Warn("door equipment missing in BuildSim; recreating")
			if recreated, e2 := a.bs.EnsureEquipment(ctx, a.blank()); e2 != nil {
				lastErr = e2
			} else {
				// Rebuild the desired object on the fresh equipment.
				want = recreated
				want.Actuators = cloneActuators(recreated.Actuators)
				want.Sensors = cloneSensors(recreated.Sensors)
				setActuatorState(&want, actuatorLock, string(lock))
			}
			continue
		}
		a.log.Warn("BuildSim write failed; will retry", "attempt", attempt+1, "err", err)
	}
	return buildsim.Equipment{}, fmt.Errorf("apply lock=%s after %d attempts: %w", lock, a.retries+1, lastErr)
}

func (a *Actuator) currentLock() model.LockState {
	if act, ok := a.current.Actuator(actuatorLock); ok {
		return model.LockState(act.State)
	}
	return model.LockLocked
}

func (a *Actuator) state(applied bool, err error) model.DoorState {
	contact := model.ContactClosed
	if s, ok := a.current.Sensor(sensorContact); ok && s.BinaryValue {
		contact = model.ContactOpen
	}
	st := model.DoorState{
		DoorID:          a.doorID,
		Lock:            a.currentLock(),
		Contact:         contact,
		Source:          Source,
		BuildSimVersion: a.current.Version,
		Applied:         applied,
	}
	if err != nil {
		st.Error = err.Error()
	}
	return st
}

func (a *Actuator) stateWithError(err error) model.DoorState {
	return a.state(false, err)
}

func (a *Actuator) emit(ctx context.Context, st model.DoorState) (model.DoorState, error) {
	env, err := model.NewEnvelope(Source, model.TypeDoorState, time.Now(), st)
	if err != nil {
		return st, fmt.Errorf("dooractuator: build door.state envelope: %w", err)
	}
	if err := a.pub.Publish(ctx, env); err != nil {
		a.log.Error("could not publish door.state", "err", err)
		return st, fmt.Errorf("dooractuator: publish door.state: %w", err)
	}
	return st, nil
}

func setActuatorState(eq *buildsim.Equipment, id, state string) {
	for i := range eq.Actuators {
		if eq.Actuators[i].ID == id {
			eq.Actuators[i].State = state
			return
		}
	}
	eq.Actuators = append(eq.Actuators, buildsim.Actuator{ID: id, State: state})
}

func cloneActuators(in []buildsim.Actuator) []buildsim.Actuator {
	out := make([]buildsim.Actuator, len(in))
	copy(out, in)
	return out
}

func cloneSensors(in []buildsim.Sensor) []buildsim.Sensor {
	out := make([]buildsim.Sensor, len(in))
	copy(out, in)
	return out
}
