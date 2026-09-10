package model

import "fmt"

// BuildSimRoom identifies a room in BuildSim. BuildSim room names are not
// globally unique (some repeat on a level), so the pair (Level, Name) is
// only safe for rooms whose name is known-unique on that level — which is
// true for every room used here (verified in ADR 0002/0003).
type BuildSimRoom struct {
	Level string
	Name  string
}

// DoorMapping ties one logical door to the BuildSim room its equipment
// lives in and to the room it controls entry to.
type DoorMapping struct {
	DoorID   string
	ReaderID string
	// ControlsRoom is the logical room a badge-in at this door grants
	// entry to.
	ControlsRoom string
	// Equipment is where the door's BuildSim equipment object sits.
	Equipment BuildSimRoom
}

// Rooms maps every logical room id to its BuildSim location.
var Rooms = map[string]BuildSimRoom{
	RoomOffice: {Level: "level0", Name: "A1006"},
	RoomLab:    {Level: "level0", Name: "A1007"},
}

// Doors maps every logical door id to its mapping. The door equipment is
// placed in the room it protects (RoomLab / A1007).
var Doors = map[string]DoorMapping{
	Door1: {
		DoorID:       Door1,
		ReaderID:     Reader1,
		ControlsRoom: RoomLab,
		Equipment:    BuildSimRoom{Level: "level0", Name: "A1007"},
	},
}

// LookupRoom returns the BuildSim location of a logical room.
func LookupRoom(roomID string) (BuildSimRoom, bool) {
	r, ok := Rooms[roomID]
	return r, ok
}

// LookupDoor returns the mapping for a logical door.
func LookupDoor(doorID string) (DoorMapping, bool) {
	d, ok := Doors[doorID]
	return d, ok
}

// Validate checks the mapping tables are internally consistent. Services
// call it at startup so a bad edit fails fast rather than at first use.
func Validate() error {
	for id, r := range Rooms {
		if r.Level == "" || r.Name == "" {
			return fmt.Errorf("model: room %q has empty level or name", id)
		}
	}
	for id, d := range Doors {
		if d.DoorID != id {
			return fmt.Errorf("model: door key %q disagrees with DoorID %q", id, d.DoorID)
		}
		if d.ReaderID == "" {
			return fmt.Errorf("model: door %q has no reader", id)
		}
		if _, ok := Rooms[d.ControlsRoom]; !ok {
			return fmt.Errorf("model: door %q controls unknown room %q", id, d.ControlsRoom)
		}
		if d.Equipment.Level == "" || d.Equipment.Name == "" {
			return fmt.Errorf("model: door %q has incomplete equipment location", id)
		}
	}
	return nil
}
