package model

// Event type identifiers. These are the only types the minimal loop
// (ADR 0003) uses. Adding one means adding a payload struct below and a
// case to knownTypes.
const (
	TypeBadgeRead         = "badge.read"
	TypeOccupancyObserved = "occupancy.observed"
	TypeAccessDecision    = "access.decision"
	TypeDoorCommand       = "door.command"
	TypeDoorState         = "door.state"
)

var knownTypes = map[string]bool{
	TypeBadgeRead:         true,
	TypeOccupancyObserved: true,
	TypeAccessDecision:    true,
	TypeDoorCommand:       true,
	TypeDoorState:         true,
}

// KnownType reports whether t is an event type this build understands.
func KnownType(t string) bool { return knownTypes[t] }

// AccessAction is the outcome the decision service selects for an access
// attempt. Detection stays separate from action: an anomaly score is
// evidence, never one of these values.
type AccessAction string

const (
	ActionAllow    AccessAction = "allow"    // grant entry, unlock briefly
	ActionRestrict AccessAction = "restrict" // deny entry, keep locked
	ActionAlert    AccessAction = "alert"    // deny entry, keep locked, raise an alert
)

// LockState is the state of a door's lock actuator.
type LockState string

const (
	LockLocked   LockState = "locked"
	LockUnlocked LockState = "unlocked"
)

// ContactState is the state of a door's contact sensor.
type ContactState string

const (
	ContactOpen   ContactState = "open"
	ContactClosed ContactState = "closed"
)

// BadgeRead is emitted when a card is presented at a reader.
type BadgeRead struct {
	ReaderID   string `json:"reader_id"`
	DoorID     string `json:"door_id"`
	RoomID     string `json:"room_id"` // the room the reader controls entry to
	CardID     string `json:"card_id"`
	OccupantID string `json:"occupant_id,omitempty"` // simulator-only, for evaluation
}

// OccupancyObserved is a presence observation for one room from one
// sensor. Partially independent evidence of movement.
type OccupancyObserved struct {
	SensorID      string `json:"sensor_id"`
	RoomID        string `json:"room_id"`
	Present       bool   `json:"present"`
	OccupantCount int    `json:"occupant_count"`
}

// DecisionEvidence records the inputs that led to an AccessDecision. It
// is always populated, so evaluation can see why the system acted and
// compare against the deterministic baseline.
type DecisionEvidence struct {
	CardPermitted  bool `json:"card_permitted"`
	WithinWindow   bool `json:"within_window"`
	RoomRestricted bool `json:"room_restricted"`
	// ObservedPresent is the room's last occupancy reading, if any.
	ObservedPresent *bool `json:"observed_present,omitempty"`
	// PolicyRule names the baseline truth-table row that fired.
	PolicyRule string `json:"policy_rule"`
}

// AccessDecision is the decision service's verdict for one badge read.
type AccessDecision struct {
	DecisionID string           `json:"decision_id"`
	CardID     string           `json:"card_id"`
	DoorID     string           `json:"door_id"`
	RoomID     string           `json:"room_id"`
	Action     AccessAction     `json:"action"`
	Reason     string           `json:"reason"`
	Evidence   DecisionEvidence `json:"evidence"`
	// BaselineAction is what the deterministic baseline alone would have
	// chosen. In phase 1 it equals Action; once the anomaly detector is
	// wired in (phase 2) the two can diverge and the difference is a
	// headline evaluation metric.
	BaselineAction AccessAction `json:"baseline_action"`
}

// DoorCommand asks the actuator to put a door's lock into a state.
type DoorCommand struct {
	CommandID   string    `json:"command_id"`
	DecisionID  string    `json:"decision_id"`
	DoorID      string    `json:"door_id"`
	DesiredLock LockState `json:"desired_lock"`
	// TTLSeconds > 0 means the state is temporary; 0 means indefinite.
	TTLSeconds int `json:"ttl_seconds"`
}

// DoorState reports the outcome of an actuation attempt. Applied=false
// with Error set is how an actuator failure is surfaced — on the bus,
// never as a dropped error.
type DoorState struct {
	DoorID          string       `json:"door_id"`
	Lock            LockState    `json:"lock"`
	Contact         ContactState `json:"contact"`
	Source          string       `json:"source"` // "actuator"
	BuildSimVersion int          `json:"buildsim_version"`
	Applied         bool         `json:"applied"`
	Error           string       `json:"error,omitempty"`
}
