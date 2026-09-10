package model

import (
	"testing"
	"time"
)

func TestNewULID_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10000; i++ {
		id := NewULID(base)
		if len(id) != 26 {
			t.Fatalf("length = %d, want 26 (%q)", len(id), id)
		}
		if !ValidULID(id) {
			t.Fatalf("ValidULID(%q) = false", id)
		}
		if seen[id] {
			t.Fatalf("duplicate ULID %q at iteration %d", id, i)
		}
		seen[id] = true
	}
}

func TestULIDTime_RoundTrip(t *testing.T) {
	for _, want := range []time.Time{
		time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC),
	} {
		id := NewULID(want)
		got, ok := ULIDTime(id)
		if !ok {
			t.Fatalf("ULIDTime(%q) not ok", id)
		}
		if !got.Equal(want.Truncate(time.Millisecond)) {
			t.Errorf("ULIDTime = %s, want %s", got, want)
		}
	}
}

func TestULID_LexicographicOrderFollowsTime(t *testing.T) {
	early := NewULID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	late := NewULID(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if !(early < late) {
		t.Fatalf("expected %q < %q", early, late)
	}
}

func TestValidULID_Rejects(t *testing.T) {
	cases := []string{
		"",
		"tooshort",
		"0000000000000000000000000000", // 28 chars
		"IIIIIIIIIIIIIIIIIIIIIIIIII",   // I is not in Crockford
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZ",   // first char > 7: overflows 128 bits
	}
	for _, c := range cases {
		if ValidULID(c) {
			t.Errorf("ValidULID(%q) = true, want false", c)
		}
	}
}
