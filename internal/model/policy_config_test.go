package model

import (
	"testing"
	"time"
)

// at returns 2026-09-10 at the given "HH:MM" UTC.
func at(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic(err)
	}
	return time.Date(2026, 9, 10, t.Hour(), t.Minute(), 0, 0, time.UTC)
}

func TestTimeWindow_Contains(t *testing.T) {
	w := TimeWindow{Start: "07:00", End: "19:00"}
	cases := []struct {
		t    string
		want bool
	}{
		{"06:59", false},
		{"07:00", true}, // inclusive start
		{"12:30", true},
		{"18:59", true},
		{"19:00", false}, // exclusive end
		{"23:00", false},
	}
	for _, c := range cases {
		if got := w.Contains(at(c.t)); got != c.want {
			t.Errorf("Contains(%s) = %v, want %v", c.t, got, c.want)
		}
	}
}

func TestTimeWindow_MalformedFailsClosed(t *testing.T) {
	if (TimeWindow{Start: "nope", End: "19:00"}).Contains(at("12:00")) {
		t.Fatal("a malformed window must contain nothing")
	}
}

func TestTimeWindow_Valid(t *testing.T) {
	if err := (TimeWindow{Start: "07:00", End: "19:00"}).Valid(); err != nil {
		t.Errorf("valid window rejected: %v", err)
	}
	if err := (TimeWindow{Start: "19:00", End: "07:00"}).Valid(); err == nil {
		t.Error("end-before-start window should be invalid")
	}
	if err := (TimeWindow{Start: "07:60", End: "19:00"}).Valid(); err == nil {
		t.Error("minute 60 should be invalid")
	}
	if err := AllDay.Valid(); err != nil {
		t.Errorf("AllDay should be valid: %v", err)
	}
}

func TestMinimalLoopPolicy(t *testing.T) {
	c := MinimalLoopPolicy()
	if err := c.Validate(); err != nil {
		t.Fatalf("MinimalLoopPolicy fails its own Validate: %v", err)
	}

	if !c.CardPermitted(CardStaff, RoomOffice) {
		t.Error("staff card should be permitted for the office")
	}
	if c.CardPermitted(CardStaff, RoomLab) {
		t.Error("no card is permitted for the lab in the minimal loop")
	}
	if !c.RoomRestricted(RoomLab) {
		t.Error("lab should be restricted")
	}
	if c.RoomRestricted(RoomOffice) {
		t.Error("office should not be restricted")
	}

	// Office has no window -> AllDay.
	if !c.WithinWindow(RoomOffice, at("03:00")) {
		t.Error("office should be enterable at any hour")
	}
	// Lab window 07:00-19:00.
	if c.WithinWindow(RoomLab, at("22:00")) {
		t.Error("lab should be closed at 22:00 (the injected anomaly time)")
	}
	if !c.WithinWindow(RoomLab, at("09:00")) {
		t.Error("lab should be open at 09:00")
	}
}

func TestPolicyConfig_Validate_UnknownRoom(t *testing.T) {
	bad := PolicyConfig{RestrictedRooms: map[string]bool{"room-ghost": true}}
	if err := bad.Validate(); err == nil {
		t.Fatal("Validate should reject an unknown restricted room")
	}
}
