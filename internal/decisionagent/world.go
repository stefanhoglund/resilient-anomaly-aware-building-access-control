package decisionagent

import (
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// world is the agent's in-memory view of the building. Every entry
// records the ordering key of the observation it came from so a later,
// stale delivery can be rejected.
type world struct {
	occupancy map[string]stampedOccupancy // room id -> latest
	doors     map[string]doorView         // door id -> latest
}

type stampedOccupancy struct {
	obs        model.OccupancyObserved
	occurredAt time.Time
}

type doorView struct {
	lock    model.LockState
	contact model.ContactState
	version int
	known   bool
}

func newWorld() *world {
	return &world{
		occupancy: make(map[string]stampedOccupancy),
		doors:     make(map[string]doorView),
	}
}

// applyOccupancy stores obs if it is newer than the room's current
// observation. It returns false if obs is stale (same or older event
// time) and was not applied.
func (w *world) applyOccupancy(obs model.OccupancyObserved, occurredAt time.Time) bool {
	if cur, ok := w.occupancy[obs.RoomID]; ok && !occurredAt.After(cur.occurredAt) {
		return false
	}
	w.occupancy[obs.RoomID] = stampedOccupancy{obs: obs, occurredAt: occurredAt}
	return true
}

// roomOccupancy returns the latest occupancy observation for a room, or
// nil if none has been seen.
func (w *world) roomOccupancy(roomID string) *model.OccupancyObserved {
	if cur, ok := w.occupancy[roomID]; ok {
		obs := cur.obs
		return &obs
	}
	return nil
}

// applyDoorState stores ds if its BuildSim version is at least the
// version already recorded for that door. It returns false if ds is stale
// and was not applied.
func (w *world) applyDoorState(ds model.DoorState) bool {
	if cur, ok := w.doors[ds.DoorID]; ok && ds.BuildSimVersion < cur.version {
		return false
	}
	w.doors[ds.DoorID] = doorView{
		lock:    ds.Lock,
		contact: ds.Contact,
		version: ds.BuildSimVersion,
		known:   true,
	}
	return true
}

// doorLock returns a door's last known lock state. Until a door.state has
// been seen it reports locked — a cold agent must never assume a door is
// open (ADR 0003).
func (w *world) doorLock(doorID string) model.LockState {
	if cur, ok := w.doors[doorID]; ok && cur.known {
		return cur.lock
	}
	return model.LockLocked
}
