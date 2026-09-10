package policy

import (
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func tstamp(hhmm string) time.Time {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		panic(err)
	}
	return time.Date(2026, 9, 10, t.Hour(), t.Minute(), 0, 0, time.UTC)
}

// A config with both a permitted room (office, all day) and a restricted,
// windowed room (lab, 07:00-19:00) that no card may enter.
func testCfg() model.PolicyConfig {
	return model.PolicyConfig{
		Permissions: map[string][]string{
			"CARD-STAFF": {model.RoomOffice},
			"CARD-LAB":   {model.RoomLab, model.RoomOffice},
		},
		RoomWindows: map[string]model.TimeWindow{
			model.RoomLab: {Start: "07:00", End: "19:00"},
		},
		RestrictedRooms: map[string]bool{model.RoomLab: true},
	}
}

func TestDecide_TruthTable(t *testing.T) {
	cfg := testCfg()

	cases := []struct {
		name       string
		card       string
		room       string
		at         string
		wantAction model.AccessAction
		wantLock   model.LockState
		wantRule   string
		wantTTL    bool // true => TTLSeconds > 0
	}{
		{
			name: "permitted, in window (all-day office)",
			card: "CARD-STAFF", room: model.RoomOffice, at: "03:00",
			wantAction: model.ActionAllow, wantLock: model.LockUnlocked,
			wantRule: RulePermittedInWindow, wantTTL: true,
		},
		{
			name: "permitted for lab, in window",
			card: "CARD-LAB", room: model.RoomLab, at: "09:00",
			wantAction: model.ActionAllow, wantLock: model.LockUnlocked,
			wantRule: RulePermittedInWindow, wantTTL: true,
		},
		{
			name: "permitted for lab, outside window",
			card: "CARD-LAB", room: model.RoomLab, at: "22:00",
			wantAction: model.ActionRestrict, wantLock: model.LockLocked,
			wantRule: RuleOutsideWindow,
		},
		{
			name: "not permitted, restricted room -> alert",
			card: "CARD-STAFF", room: model.RoomLab, at: "09:00",
			wantAction: model.ActionAlert, wantLock: model.LockLocked,
			wantRule: RuleRestrictedViolation,
		},
		{
			name: "not permitted, restricted room, also outside window -> alert",
			card: "CARD-STAFF", room: model.RoomLab, at: "23:30",
			wantAction: model.ActionAlert, wantLock: model.LockLocked,
			wantRule: RuleRestrictedViolation,
		},
		{
			name: "unknown card, non-restricted room -> restrict",
			card: "CARD-GHOST", room: model.RoomOffice, at: "12:00",
			wantAction: model.ActionRestrict, wantLock: model.LockLocked,
			wantRule: RuleNotPermitted,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := Request{CardID: c.card, DoorID: model.Door1, RoomID: c.room, OccurredAt: tstamp(c.at)}
			d, cmd := Decide(cfg, req)

			if d.Action != c.wantAction {
				t.Errorf("Action = %q, want %q", d.Action, c.wantAction)
			}
			if d.BaselineAction != d.Action {
				t.Errorf("BaselineAction %q != Action %q (must match in phase 1)", d.BaselineAction, d.Action)
			}
			if d.Evidence.PolicyRule != c.wantRule {
				t.Errorf("PolicyRule = %q, want %q", d.Evidence.PolicyRule, c.wantRule)
			}
			if cmd.DesiredLock != c.wantLock {
				t.Errorf("DesiredLock = %q, want %q", cmd.DesiredLock, c.wantLock)
			}
			if (cmd.TTLSeconds > 0) != c.wantTTL {
				t.Errorf("TTLSeconds = %d, wantPositive=%v", cmd.TTLSeconds, c.wantTTL)
			}
			if cmd.DoorID != model.Door1 {
				t.Errorf("cmd.DoorID = %q", cmd.DoorID)
			}
			if d.Reason == "" {
				t.Error("decision has empty reason")
			}
		})
	}
}

func TestDecide_WindowBoundariesInclusiveStartExclusiveEnd(t *testing.T) {
	cfg := testCfg()
	mk := func(at string) model.AccessDecision {
		d, _ := Decide(cfg, Request{
			CardID: "CARD-LAB", DoorID: model.Door1, RoomID: model.RoomLab, OccurredAt: tstamp(at),
		})
		return d
	}
	if got := mk("07:00").Action; got != model.ActionAllow {
		t.Errorf("07:00 (window start) -> %q, want allow", got)
	}
	if got := mk("19:00").Action; got != model.ActionRestrict {
		t.Errorf("19:00 (window end) -> %q, want restrict", got)
	}
	if got := mk("18:59").Action; got != model.ActionAllow {
		t.Errorf("18:59 -> %q, want allow", got)
	}
}

func TestDecide_RecordsOccupancyEvidence(t *testing.T) {
	cfg := testCfg()
	req := Request{CardID: "CARD-STAFF", DoorID: model.Door1, RoomID: model.RoomOffice, OccurredAt: tstamp("12:00")}

	d, _ := Decide(cfg, req)
	if d.Evidence.ObservedPresent != nil {
		t.Error("ObservedPresent should be nil when no occupancy given")
	}

	req.Occupancy = &model.OccupancyObserved{RoomID: model.RoomOffice, Present: true}
	d, _ = Decide(cfg, req)
	if d.Evidence.ObservedPresent == nil || !*d.Evidence.ObservedPresent {
		t.Errorf("ObservedPresent = %v, want true", d.Evidence.ObservedPresent)
	}
}

func TestDecide_PermittedForRestrictedRoomIsStillAllowed(t *testing.T) {
	cfg := testCfg()
	d, cmd := Decide(cfg, Request{
		CardID: "CARD-LAB", DoorID: model.Door1, RoomID: model.RoomLab, OccurredAt: tstamp("10:00"),
	})
	if d.Action != model.ActionAllow || cmd.DesiredLock != model.LockUnlocked {
		t.Fatalf("permitted card for restricted room in window should be allowed, got %q/%q", d.Action, cmd.DesiredLock)
	}
}

func TestRequest_Validate(t *testing.T) {
	good := Request{CardID: "c", DoorID: "d", RoomID: "r", OccurredAt: time.Now()}
	if err := good.Validate(); err != nil {
		t.Fatalf("good request rejected: %v", err)
	}
	for _, bad := range []Request{
		{DoorID: "d", RoomID: "r", OccurredAt: time.Now()},
		{CardID: "c", RoomID: "r", OccurredAt: time.Now()},
		{CardID: "c", DoorID: "d", OccurredAt: time.Now()},
		{CardID: "c", DoorID: "d", RoomID: "r"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

// The phase-1 scenario: the injected anomaly (staff card at the lab,
// 22:00) must resolve to an alert with the door left locked.
func TestDecide_MinimalLoopAnomaly(t *testing.T) {
	cfg := model.MinimalLoopPolicy()
	d, cmd := Decide(cfg, Request{
		CardID:     model.CardStaff,
		DoorID:     model.Door1,
		RoomID:     model.RoomLab,
		OccurredAt: tstamp("22:00"),
	})
	if d.Action != model.ActionAlert {
		t.Fatalf("injected anomaly -> %q, want alert", d.Action)
	}
	if cmd.DesiredLock != model.LockLocked {
		t.Fatalf("anomaly should keep the door locked, got %q", cmd.DesiredLock)
	}

	// And the normal case: staff into the office is allowed.
	d, _ = Decide(cfg, Request{
		CardID: model.CardStaff, DoorID: model.Door1, RoomID: model.RoomOffice, OccurredAt: tstamp("09:00"),
	})
	if d.Action != model.ActionAllow {
		t.Fatalf("staff into office -> %q, want allow", d.Action)
	}
}
