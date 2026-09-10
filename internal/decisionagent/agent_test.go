package decisionagent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

type fakePub struct {
	mu   sync.Mutex
	envs []model.Envelope
	err  error
}

func (p *fakePub) Publish(_ context.Context, env model.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.envs = append(p.envs, env)
	return nil
}

func (p *fakePub) byType(t *testing.T, typ string) []model.Envelope {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []model.Envelope
	for _, e := range p.envs {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func newAgent(t *testing.T, pub Publisher) *Agent {
	t.Helper()
	a, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC) },
	}, pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func badge(t *testing.T, card, room string, at time.Time) model.Envelope {
	t.Helper()
	e, err := model.NewEnvelope("simulator", model.TypeBadgeRead, at, model.BadgeRead{
		ReaderID: model.Reader1, DoorID: model.Door1, RoomID: room, CardID: card,
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return e
}

func occ(t *testing.T, room string, present bool, at time.Time) model.Envelope {
	t.Helper()
	e, err := model.NewEnvelope("simulator", model.TypeOccupancyObserved, at, model.OccupancyObserved{
		SensorID: "occ-1", RoomID: room, Present: present,
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return e
}

func doorState(t *testing.T, lock model.LockState, version int, at time.Time) model.Envelope {
	t.Helper()
	e, err := model.NewEnvelope("door-actuator", model.TypeDoorState, at, model.DoorState{
		DoorID: model.Door1, Lock: lock, Contact: model.ContactClosed,
		Source: "actuator", BuildSimVersion: version, Applied: true,
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return e
}

func decodeDecision(t *testing.T, env model.Envelope) model.AccessDecision {
	t.Helper()
	d, err := model.Decode[model.AccessDecision](env)
	if err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	return d
}

func decodeCommand(t *testing.T, env model.Envelope) model.DoorCommand {
	t.Helper()
	c, err := model.Decode[model.DoorCommand](env)
	if err != nil {
		t.Fatalf("decode command: %v", err)
	}
	return c
}

// A permitted badge into the office (any hour) -> allow + a command to
// unlock, because the door is assumed locked.
func TestHandle_AllowUnlocksAssumedLockedDoor(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)

	err := a.Handle(context.Background(),
		badge(t, model.CardStaff, model.RoomOffice, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	decs := pub.byType(t, model.TypeAccessDecision)
	if len(decs) != 1 {
		t.Fatalf("want 1 access.decision, got %d", len(decs))
	}
	d := decodeDecision(t, decs[0])
	if d.Action != model.ActionAllow || d.DecisionID == "" {
		t.Fatalf("decision = %+v, want allow with an id", d)
	}

	cmds := pub.byType(t, model.TypeDoorCommand)
	if len(cmds) != 1 {
		t.Fatalf("want 1 door.command, got %d", len(cmds))
	}
	c := decodeCommand(t, cmds[0])
	if c.DesiredLock != model.LockUnlocked || c.DecisionID != d.DecisionID || c.CommandID == "" {
		t.Fatalf("command = %+v, want unlock linked to decision %s", c, d.DecisionID)
	}
}

// The injected anomaly: staff card at the restricted lab at 22:00 ->
// alert, and NO command because the door is already (assumed) locked.
func TestHandle_AnomalyAlertsWithoutCommand(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)

	err := a.Handle(context.Background(),
		badge(t, model.CardStaff, model.RoomLab, time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	decs := pub.byType(t, model.TypeAccessDecision)
	if len(decs) != 1 || decodeDecision(t, decs[0]).Action != model.ActionAlert {
		t.Fatalf("want a single alert decision, got %+v", decs)
	}
	if cmds := pub.byType(t, model.TypeDoorCommand); len(cmds) != 0 {
		t.Fatalf("alert on an already-locked door should issue no command, got %d", len(cmds))
	}
}

func TestHandle_DuplicateBadgeReadIgnored(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	b := badge(t, model.CardStaff, model.RoomOffice, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	if err := a.Handle(ctx, b); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := a.Handle(ctx, b); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := pub.byType(t, model.TypeAccessDecision); len(got) != 1 {
		t.Fatalf("duplicate badge.read produced %d decisions, want 1", len(got))
	}
}

func TestHandle_AnomalyCommandsLockWhenDoorIsUnlocked(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	// The agent learns the door is currently unlocked.
	if err := a.Handle(ctx, doorState(t, model.LockUnlocked, 5, time.Now())); err != nil {
		t.Fatalf("door.state: %v", err)
	}

	if err := a.Handle(ctx,
		badge(t, model.CardStaff, model.RoomLab, time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("badge: %v", err)
	}

	cmds := pub.byType(t, model.TypeDoorCommand)
	if len(cmds) != 1 || decodeCommand(t, cmds[0]).DesiredLock != model.LockLocked {
		t.Fatalf("want one lock command, got %+v", cmds)
	}
}

func TestHandle_NoCommandWhenDoorAlreadyInDesiredState(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	if err := a.Handle(ctx, doorState(t, model.LockUnlocked, 5, time.Now())); err != nil {
		t.Fatalf("door.state: %v", err)
	}
	// Permitted badge -> allow -> wants unlocked, which it already is.
	if err := a.Handle(ctx,
		badge(t, model.CardStaff, model.RoomOffice, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("badge: %v", err)
	}

	if got := pub.byType(t, model.TypeAccessDecision); len(got) != 1 {
		t.Fatalf("want the decision to still be published, got %d", len(got))
	}
	if got := pub.byType(t, model.TypeDoorCommand); len(got) != 0 {
		t.Fatalf("no command expected when the lock already matches, got %d", len(got))
	}
}

func TestHandle_OccupancyEvidenceReachesDecision(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	if err := a.Handle(ctx, occ(t, model.RoomOffice, true, time.Date(2026, 9, 10, 8, 59, 0, 0, time.UTC))); err != nil {
		t.Fatalf("occ: %v", err)
	}
	if err := a.Handle(ctx,
		badge(t, model.CardStaff, model.RoomOffice, time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("badge: %v", err)
	}

	d := decodeDecision(t, pub.byType(t, model.TypeAccessDecision)[0])
	if d.Evidence.ObservedPresent == nil || !*d.Evidence.ObservedPresent {
		t.Fatalf("decision evidence should carry observed presence, got %+v", d.Evidence)
	}
}

func TestHandle_StaleOccupancyDoesNotChangeEvidence(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	newer := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Minute)

	// Newest first, then a delayed older observation with a different value.
	_ = a.Handle(ctx, occ(t, model.RoomOffice, true, newer))
	_ = a.Handle(ctx, occ(t, model.RoomOffice, false, older))

	if got := a.world.roomOccupancy(model.RoomOffice); got == nil || !got.Present {
		t.Fatalf("stale observation overwrote fresh state: %+v", got)
	}
}

func TestHandle_SkipsUnknownAndUnparseable(t *testing.T) {
	pub := &fakePub{}
	a := newAgent(t, pub)
	ctx := context.Background()

	// Unknown type.
	unk, _ := model.NewEnvelope("x", "badge.teleported", time.Now(), map[string]string{"a": "b"})
	if err := a.Handle(ctx, unk); err != nil {
		t.Fatalf("unknown type should be skipped, not error: %v", err)
	}

	// Known type, wrong payload shape.
	bad := model.Envelope{
		ID: model.NewID(), Type: model.TypeBadgeRead, Source: "x",
		OccurredAt: time.Now(), SpecVersion: model.SpecVersion,
		Data: []byte(`"not an object"`),
	}
	if err := a.Handle(ctx, bad); err == nil {
		t.Fatal("bad payload should return an error")
	}
	if len(pub.byType(t, model.TypeAccessDecision)) != 0 {
		t.Fatal("nothing should have been published")
	}
}
