//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// TestContract_EquipmentLifecycle exercises the equipment API against the
// live BuildSim: the client's create / get / put / delete must behave as
// internal/buildsim assumes, and the global version must advance on each
// write. It uses a uniquely named probe equipment and always cleans up.
func TestContract_EquipmentLifecycle(t *testing.T) {
	c := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	id := fmt.Sprintf("it-probe-%d", time.Now().UnixNano())
	room := model.Rooms[model.RoomLab] // level0 / A1007

	eq := buildsim.Equipment{
		ID: id, Name: "integration probe", Type: "door",
		Level: room.Level, Room: room.Name,
		Sensors: []buildsim.Sensor{
			{ID: "contact", Name: "Contact", DataType: buildsim.DataTypeBinary},
			{ID: "badge", Name: "Badge", DataType: buildsim.DataTypeText},
		},
		Actuators: []buildsim.Actuator{{ID: "lock", Name: "Lock", State: "locked"}},
	}
	t.Cleanup(func() {
		_ = c.DeleteEquipment(context.Background(), id)
	})

	created, err := c.CreateEquipment(ctx, eq)
	if err != nil {
		t.Fatalf("CreateEquipment: %v", err)
	}
	if created.Version == 0 {
		t.Fatalf("created equipment has no version: %+v", created)
	}

	// Creating again must conflict.
	if _, err := c.CreateEquipment(ctx, eq); !errors.Is(err, buildsim.ErrEquipmentExists) {
		t.Fatalf("duplicate create err = %v, want ErrEquipmentExists", err)
	}

	// EnsureEquipment must return the existing object, not error.
	ensured, err := c.EnsureEquipment(ctx, eq)
	if err != nil {
		t.Fatalf("EnsureEquipment: %v", err)
	}
	if ensured.ID != id {
		t.Fatalf("EnsureEquipment returned %q", ensured.ID)
	}

	// Replace: flip the lock and a sensor; version must advance.
	upd := eq
	upd.Actuators = []buildsim.Actuator{{ID: "lock", Name: "Lock", State: "unlocked"}}
	upd.Sensors = []buildsim.Sensor{
		{ID: "contact", Name: "Contact", DataType: buildsim.DataTypeBinary, BinaryValue: true},
		{ID: "badge", Name: "Badge", DataType: buildsim.DataTypeText, Value: "CARD-001"},
	}
	put, err := c.PutEquipment(ctx, upd)
	if err != nil {
		t.Fatalf("PutEquipment: %v", err)
	}
	if put.Version <= created.Version {
		t.Fatalf("version did not advance: created=%d put=%d", created.Version, put.Version)
	}
	if a, ok := put.Actuator("lock"); !ok || a.State != "unlocked" {
		t.Fatalf("lock actuator = %+v, want state unlocked", a)
	}
	if s, ok := put.Sensor("badge"); !ok || s.Value != "CARD-001" {
		t.Fatalf("badge sensor = %+v, want value CARD-001", s)
	}

	// Read back and confirm it persisted.
	got, err := c.GetEquipment(ctx, id)
	if err != nil {
		t.Fatalf("GetEquipment: %v", err)
	}
	if got.Version != put.Version {
		t.Fatalf("read-back version = %d, want %d", got.Version, put.Version)
	}

	// Delete, then a second delete must be a no-op.
	if err := c.DeleteEquipment(ctx, id); err != nil {
		t.Fatalf("DeleteEquipment: %v", err)
	}
	if err := c.DeleteEquipment(ctx, id); err != nil {
		t.Fatalf("idempotent delete failed: %v", err)
	}
	if _, err := c.GetEquipment(ctx, id); !errors.Is(err, buildsim.ErrEquipmentNotFound) {
		t.Fatalf("get after delete err = %v, want ErrEquipmentNotFound", err)
	}
}
