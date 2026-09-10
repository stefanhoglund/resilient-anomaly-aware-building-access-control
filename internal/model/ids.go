package model

// Logical identifiers used across every service and every event. They
// are stable strings owned by us; they never contain BuildSim's own room
// names. The translation to BuildSim lives in mapping.go so that a change
// to the physical building touches exactly one place.
//
// The minimal loop (ADR 0003) uses one occupant, two rooms and one door.
const (
	// RoomOffice is a normally accessible office (BuildSim level0 / A1006).
	RoomOffice = "room-office"
	// RoomLab is a restricted lab (BuildSim level0 / A1007).
	RoomLab = "room-lab"

	// Door1 is the controlled door governing entry to RoomLab.
	Door1 = "door-1"
	// Reader1 is the badge reader mounted at Door1.
	Reader1 = "reader-1"
)

// Card identifiers for the minimal-loop scenario. Real deployments would
// load these from configuration; the scenario is fixed here so a run is
// reproducible.
const (
	// CardStaff belongs to the occupant who may enter the office.
	CardStaff = "CARD-001"
)
