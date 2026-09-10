package dooractuator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// --- fakes ---

type fakeBS struct {
	mu          sync.Mutex
	store       map[string]buildsim.Equipment
	version     int
	putErrs     []error // consumed one per PutEquipment call; nil == success
	putCalls    int
	ensureCalls int
	notFoundPut int // return ErrEquipmentNotFound for the first N puts
}

func newFakeBS() *fakeBS { return &fakeBS{store: map[string]buildsim.Equipment{}} }

func (f *fakeBS) EnsureEquipment(_ context.Context, eq buildsim.Equipment) (buildsim.Equipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls++
	if cur, ok := f.store[eq.ID]; ok {
		return cur, nil
	}
	f.version++
	eq.Version = f.version
	f.store[eq.ID] = eq
	return eq, nil
}

func (f *fakeBS) PutEquipment(_ context.Context, eq buildsim.Equipment) (buildsim.Equipment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++

	if f.notFoundPut > 0 {
		f.notFoundPut--
		delete(f.store, eq.ID)
		return buildsim.Equipment{}, buildsim.ErrEquipmentNotFound
	}
	if len(f.putErrs) > 0 {
		err := f.putErrs[0]
		f.putErrs = f.putErrs[1:]
		if err != nil {
			return buildsim.Equipment{}, err
		}
	}
	f.version++
	eq.Version = f.version
	f.store[eq.ID] = eq
	return eq, nil
}

func (f *fakeBS) lockState(t *testing.T, id string) model.LockState {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	eq, ok := f.store[id]
	if !ok {
		t.Fatalf("equipment %q not in fake BuildSim", id)
	}
	a, _ := eq.Actuator("lock")
	return model.LockState(a.State)
}

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

func (p *fakePub) states(t *testing.T) []model.DoorState {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []model.DoorState
	for _, e := range p.envs {
		if e.Type != model.TypeDoorState {
			continue
		}
		ds, err := model.Decode[model.DoorState](e)
		if err != nil {
			t.Fatalf("decode door.state: %v", err)
		}
		out = append(out, ds)
	}
	return out
}

func (p *fakePub) lastState(t *testing.T) model.DoorState {
	t.Helper()
	s := p.states(t)
	if len(s) == 0 {
		t.Fatal("no door.state published")
	}
	return s[len(s)-1]
}

// --- helpers ---

func newActuator(t *testing.T, bs BuildSim, pub Publisher) *Actuator {
	t.Helper()
	a, err := New(Config{
		DoorID:    model.Door1,
		Retries:   3,
		RetryWait: time.Millisecond,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, bs, pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func cmd(id string, lock model.LockState) model.DoorCommand {
	return model.DoorCommand{
		CommandID:   id,
		DecisionID:  "dec-" + id,
		DoorID:      model.Door1,
		DesiredLock: lock,
	}
}

// --- tests ---

func TestHandle_AppliesLockChange(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)
	ctx := context.Background()

	st, err := a.Handle(ctx, cmd("c1", model.LockUnlocked))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !st.Applied || st.Lock != model.LockUnlocked {
		t.Fatalf("state = %+v, want applied unlocked", st)
	}
	if bs.lockState(t, model.Door1) != model.LockUnlocked {
		t.Fatal("BuildSim lock not updated")
	}
	if st.Source != Source || st.BuildSimVersion == 0 {
		t.Fatalf("state missing source/version: %+v", st)
	}
}

func TestHandle_DuplicateCommandDoesNotWriteTwice(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)
	ctx := context.Background()

	if _, err := a.Handle(ctx, cmd("dup", model.LockUnlocked)); err != nil {
		t.Fatalf("first: %v", err)
	}
	putsAfterFirst := bs.putCalls

	if _, err := a.Handle(ctx, cmd("dup", model.LockUnlocked)); err != nil {
		t.Fatalf("second: %v", err)
	}
	if bs.putCalls != putsAfterFirst {
		t.Fatalf("duplicate command triggered another write (%d -> %d)", putsAfterFirst, bs.putCalls)
	}
	if got := pub.states(t); len(got) != 2 {
		t.Fatalf("want 2 door.state emissions, got %d", len(got))
	}
}

func TestHandle_NoOpWhenAlreadyInDesiredState(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)
	ctx := context.Background()

	// The door starts locked; command it locked.
	st, err := a.Handle(ctx, cmd("c1", model.LockLocked))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if bs.putCalls != 0 {
		t.Fatalf("no-op command still wrote BuildSim (%d puts)", bs.putCalls)
	}
	if !st.Applied || st.Lock != model.LockLocked {
		t.Fatalf("state = %+v, want applied locked", st)
	}
}

func TestHandle_WrongDoor(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)

	bad := cmd("c1", model.LockUnlocked)
	bad.DoorID = "door-other"
	st, _ := a.Handle(context.Background(), bad)
	if st.Applied || st.Error == "" {
		t.Fatalf("state = %+v, want applied=false with an error", st)
	}
	if bs.putCalls != 0 {
		t.Fatal("wrong-door command wrote BuildSim")
	}
}

func TestHandle_RetriesThenSucceeds(t *testing.T) {
	bs := newFakeBS()
	bs.putErrs = []error{errors.New("boom"), errors.New("boom"), nil}
	pub := &fakePub{}
	a := newActuator(t, bs, pub)

	st, err := a.Handle(context.Background(), cmd("c1", model.LockUnlocked))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !st.Applied {
		t.Fatalf("expected eventual success, got %+v", st)
	}
	if bs.putCalls != 3 {
		t.Fatalf("put calls = %d, want 3", bs.putCalls)
	}
}

func TestHandle_ActuatorFailureIsReportedNotFatal(t *testing.T) {
	bs := newFakeBS()
	bs.putErrs = []error{
		errors.New("boom"), errors.New("boom"), errors.New("boom"), errors.New("boom"),
	}
	pub := &fakePub{}
	a := newActuator(t, bs, pub)
	ctx := context.Background()

	st, err := a.Handle(ctx, cmd("c1", model.LockUnlocked))
	if err != nil {
		t.Fatalf("Handle should not return an error for an actuator failure: %v", err)
	}
	if st.Applied || st.Error == "" {
		t.Fatalf("state = %+v, want applied=false with error", st)
	}
	// The door is still locked (the failed change did not take), and a
	// later command succeeds — the actuator kept serving.
	if _, err := a.Handle(ctx, cmd("c2", model.LockUnlocked)); err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	if got := pub.lastState(t); !got.Applied || got.Lock != model.LockUnlocked {
		t.Fatalf("recovery state = %+v, want applied unlocked", got)
	}
}

func TestHandle_RecreatesEquipmentOn404(t *testing.T) {
	bs := newFakeBS()
	bs.notFoundPut = 1 // first PUT reports the equipment vanished
	pub := &fakePub{}
	a := newActuator(t, bs, pub)

	st, err := a.Handle(context.Background(), cmd("c1", model.LockUnlocked))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !st.Applied || st.Lock != model.LockUnlocked {
		t.Fatalf("state = %+v, want applied unlocked after recreate", st)
	}
	if bs.ensureCalls < 2 {
		t.Fatalf("expected a re-ensure after 404, ensureCalls=%d", bs.ensureCalls)
	}
}

func TestHandle_LazyEnsureWhenNotPreRegistered(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)
	// No EnsureReady call.
	if _, err := a.Handle(context.Background(), cmd("c1", model.LockUnlocked)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if bs.ensureCalls == 0 {
		t.Fatal("Handle did not lazily register the equipment")
	}
}

func TestEmit_PublishFailureSurfacesAsError(t *testing.T) {
	bs := newFakeBS()
	pub := &fakePub{err: errors.New("bus down")}
	a := newActuator(t, bs, pub)

	st, err := a.Handle(context.Background(), cmd("c1", model.LockUnlocked))
	if err == nil {
		t.Fatal("expected a publish error to be returned")
	}
	// The actuation still happened even though the report could not be sent.
	if !st.Applied || bs.lockState(t, model.Door1) != model.LockUnlocked {
		t.Fatalf("actuation should have applied: %+v", st)
	}
}

// The phase-1 anomaly path: an alert decision produces a door.command to
// stay locked; the door is already locked, so it is a no-op that still
// reports state.
func TestHandle_MinimalLoopAnomalyKeepsLocked(t *testing.T) {
	bs, pub := newFakeBS(), &fakePub{}
	a := newActuator(t, bs, pub)
	if err := a.EnsureReady(context.Background()); err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}

	st, err := a.Handle(context.Background(), cmd("alert-cmd", model.LockLocked))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if st.Lock != model.LockLocked || !st.Applied {
		t.Fatalf("state = %+v, want applied locked", st)
	}
	if bs.putCalls != 0 {
		t.Fatalf("anomaly no-op wrote BuildSim %d times", bs.putCalls)
	}
}
