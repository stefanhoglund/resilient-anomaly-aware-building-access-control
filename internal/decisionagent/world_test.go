package decisionagent

import (
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func TestWorld_OccupancyStaleRejected(t *testing.T) {
	w := newWorld()
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	if !w.applyOccupancy(model.OccupancyObserved{RoomID: model.RoomLab, Present: true}, t0) {
		t.Fatal("first observation should apply")
	}
	// Older -> rejected.
	if w.applyOccupancy(model.OccupancyObserved{RoomID: model.RoomLab, Present: false}, t0.Add(-time.Second)) {
		t.Fatal("older observation should be rejected")
	}
	// Same timestamp -> rejected (idempotent).
	if w.applyOccupancy(model.OccupancyObserved{RoomID: model.RoomLab, Present: false}, t0) {
		t.Fatal("same-timestamp observation should be rejected")
	}
	// Newer -> applied.
	if !w.applyOccupancy(model.OccupancyObserved{RoomID: model.RoomLab, Present: false}, t0.Add(time.Second)) {
		t.Fatal("newer observation should apply")
	}
	if got := w.roomOccupancy(model.RoomLab); got == nil || got.Present {
		t.Fatalf("latest occupancy = %+v, want present=false", got)
	}
}

func TestWorld_DoorStateVersionOrdering(t *testing.T) {
	w := newWorld()
	if !w.applyDoorState(model.DoorState{DoorID: model.Door1, Lock: model.LockUnlocked, BuildSimVersion: 10}) {
		t.Fatal("first door.state should apply")
	}
	if w.applyDoorState(model.DoorState{DoorID: model.Door1, Lock: model.LockLocked, BuildSimVersion: 9}) {
		t.Fatal("lower-version door.state should be rejected")
	}
	if w.doorLock(model.Door1) != model.LockUnlocked {
		t.Fatal("stale door.state must not have changed the lock view")
	}
	if !w.applyDoorState(model.DoorState{DoorID: model.Door1, Lock: model.LockLocked, BuildSimVersion: 11}) {
		t.Fatal("higher-version door.state should apply")
	}
	if w.doorLock(model.Door1) != model.LockLocked {
		t.Fatal("door lock view should be locked after the newer state")
	}
}

func TestWorld_UnknownDoorAssumedLocked(t *testing.T) {
	w := newWorld()
	if w.doorLock("door-unseen") != model.LockLocked {
		t.Fatal("an unseen door must be assumed locked")
	}
}
