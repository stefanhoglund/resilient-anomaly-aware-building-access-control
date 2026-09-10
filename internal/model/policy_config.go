package model

import (
	"fmt"
	"time"
)

// TimeWindow is a daily allowed interval in wall-clock hours and minutes,
// evaluated in the location of the timestamp it is checked against. Start
// and End are "HH:MM". A window that does not wrap midnight has Start <
// End; Start == End means "closed" and an all-day window is "00:00" to
// "24:00".
type TimeWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// AllDay is the window that always contains any time.
var AllDay = TimeWindow{Start: "00:00", End: "24:00"}

func parseHM(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("model: bad time %q: %w", s, err)
	}
	if h < 0 || m < 0 || m > 59 || h > 24 || (h == 24 && m != 0) {
		return 0, fmt.Errorf("model: time %q out of range", s)
	}
	return h*60 + m, nil
}

// Valid reports whether the window's endpoints parse and are ordered.
func (w TimeWindow) Valid() error {
	s, err := parseHM(w.Start)
	if err != nil {
		return err
	}
	e, err := parseHM(w.End)
	if err != nil {
		return err
	}
	if e < s {
		return fmt.Errorf("model: window end %s is before start %s", w.End, w.Start)
	}
	return nil
}

// Contains reports whether t's local time of day falls within the window.
// A malformed window contains nothing (fail closed).
func (w TimeWindow) Contains(t time.Time) bool {
	s, err := parseHM(w.Start)
	if err != nil {
		return false
	}
	e, err := parseHM(w.End)
	if err != nil {
		return false
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= s && mins < e
}

// PolicyConfig is the static access policy: who may enter where, when,
// and which rooms escalate to an alert on a violation. It is data, not
// logic; internal/policy turns it into an action.
type PolicyConfig struct {
	// Permissions maps a card id to the set of room ids it may enter.
	Permissions map[string][]string `json:"permissions"`
	// RoomWindows maps a room id to its allowed entry window. A room with
	// no entry is treated as AllDay.
	RoomWindows map[string]TimeWindow `json:"room_windows"`
	// RestrictedRooms is the set of room ids where a non-permitted
	// attempt raises an alert rather than a silent restrict.
	RestrictedRooms map[string]bool `json:"restricted_rooms"`
}

// CardPermitted reports whether card may enter room per Permissions.
func (c PolicyConfig) CardPermitted(card, room string) bool {
	for _, r := range c.Permissions[card] {
		if r == room {
			return true
		}
	}
	return false
}

// WindowFor returns the entry window for a room, defaulting to AllDay.
func (c PolicyConfig) WindowFor(room string) TimeWindow {
	if w, ok := c.RoomWindows[room]; ok {
		return w
	}
	return AllDay
}

// WithinWindow reports whether time t is inside room's entry window.
func (c PolicyConfig) WithinWindow(room string, t time.Time) bool {
	return c.WindowFor(room).Contains(t)
}

// RoomRestricted reports whether room escalates violations to an alert.
func (c PolicyConfig) RoomRestricted(room string) bool {
	return c.RestrictedRooms[room]
}

// Validate checks every window parses and every referenced room is known
// to the mapping tables.
func (c PolicyConfig) Validate() error {
	for room, w := range c.RoomWindows {
		if _, ok := Rooms[room]; !ok {
			return fmt.Errorf("model: policy window for unknown room %q", room)
		}
		if err := w.Valid(); err != nil {
			return err
		}
	}
	for room := range c.RestrictedRooms {
		if _, ok := Rooms[room]; !ok {
			return fmt.Errorf("model: restricted room %q is unknown", room)
		}
	}
	for card, rooms := range c.Permissions {
		for _, room := range rooms {
			if _, ok := Rooms[room]; !ok {
				return fmt.Errorf("model: card %q permitted for unknown room %q", card, room)
			}
		}
	}
	return nil
}

// MinimalLoopPolicy is the fixed policy for the phase-1 scenario
// (ADR 0003): the staff card may enter the office at any time; the lab is
// restricted and only open 07:00–19:00, and no card is permitted for it —
// so the injected out-of-hours lab attempt lands on "alert".
func MinimalLoopPolicy() PolicyConfig {
	return PolicyConfig{
		Permissions: map[string][]string{
			CardStaff: {RoomOffice},
		},
		RoomWindows: map[string]TimeWindow{
			RoomLab: {Start: "07:00", End: "19:00"},
		},
		RestrictedRooms: map[string]bool{
			RoomLab: true,
		},
	}
}
