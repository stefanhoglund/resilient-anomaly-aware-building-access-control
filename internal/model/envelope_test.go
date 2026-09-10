package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewEnvelope_RoundTrip(t *testing.T) {
	occ := time.Date(2026, 9, 10, 8, 30, 0, 0, time.UTC)
	in := BadgeRead{ReaderID: Reader1, DoorID: Door1, RoomID: RoomLab, CardID: CardStaff}

	env, err := NewEnvelope("simulator", TypeBadgeRead, occ, in)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if env.SpecVersion != SpecVersion || env.Source != "simulator" || env.Type != TypeBadgeRead {
		t.Fatalf("unexpected envelope header: %+v", env)
	}
	if !env.OccurredAt.Equal(occ) {
		t.Errorf("OccurredAt = %s, want %s", env.OccurredAt, occ)
	}

	// Marshal and unmarshal the whole envelope as it would cross the bus.
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, err := Decode[BadgeRead](back)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != in {
		t.Errorf("payload round-trip = %+v, want %+v", got, in)
	}
}

func TestEnvelope_Validate_Rejects(t *testing.T) {
	good, _ := NewEnvelope("simulator", TypeBadgeRead, time.Now(), BadgeRead{})

	tests := map[string]func(*Envelope){
		"bad id":       func(e *Envelope) { e.ID = "not-a-ulid" },
		"empty type":   func(e *Envelope) { e.Type = "" },
		"empty source": func(e *Envelope) { e.Source = "" },
		"zero time":    func(e *Envelope) { e.OccurredAt = time.Time{} },
		"zero spec":    func(e *Envelope) { e.SpecVersion = 0 },
		"empty data":   func(e *Envelope) { e.Data = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			e := good
			mutate(&e)
			if err := e.Validate(); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestEnvelope_Understandable(t *testing.T) {
	env, _ := NewEnvelope("x", TypeBadgeRead, time.Now(), BadgeRead{})
	if !env.Understandable() {
		t.Fatal("current-version known type should be understandable")
	}

	future := env
	future.SpecVersion = SpecVersion + 1
	if future.Understandable() {
		t.Fatal("a newer spec_version must not be understandable")
	}

	unknown := env
	unknown.Type = "badge.teleported"
	if unknown.Understandable() {
		t.Fatal("an unknown type must not be understandable")
	}
}

func TestDecode_WrongShape(t *testing.T) {
	env, _ := NewEnvelope("x", TypeOccupancyObserved, time.Now(),
		OccupancyObserved{RoomID: RoomLab, Present: true})
	// Decoding an object into a string field errors.
	if _, err := Decode[string](env); err == nil {
		t.Fatal("expected decode error for mismatched payload type")
	}
}
