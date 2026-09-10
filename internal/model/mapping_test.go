package model

import "testing"

func TestValidate_DefaultTablesConsistent(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("built-in mapping tables are inconsistent: %v", err)
	}
}

func TestLookup(t *testing.T) {
	if r, ok := LookupRoom(RoomLab); !ok || r.Name != "A1007" || r.Level != "level0" {
		t.Fatalf("LookupRoom(RoomLab) = %+v, %v", r, ok)
	}
	if _, ok := LookupRoom("room-nonexistent"); ok {
		t.Fatal("LookupRoom of unknown room should be !ok")
	}

	d, ok := LookupDoor(Door1)
	if !ok {
		t.Fatal("LookupDoor(Door1) not ok")
	}
	if d.ControlsRoom != RoomLab || d.ReaderID != Reader1 {
		t.Fatalf("Door1 mapping unexpected: %+v", d)
	}
	if d.Equipment.Name != "A1007" {
		t.Fatalf("Door1 equipment room = %q, want A1007", d.Equipment.Name)
	}
}

func TestValidate_CatchesBadEdit(t *testing.T) {
	// Mutate a copy of the tables via defer-restore to prove Validate
	// actually rejects a broken mapping.
	orig := Doors[Door1]
	defer func() { Doors[Door1] = orig }()

	broken := orig
	broken.ControlsRoom = "room-does-not-exist"
	Doors[Door1] = broken

	if err := Validate(); err == nil {
		t.Fatal("Validate should reject a door controlling an unknown room")
	}
}
