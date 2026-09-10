package dedupe

import "testing"

func TestSet_AddAndSeen(t *testing.T) {
	s := New(3)
	if s.Seen("a") {
		t.Fatal("empty set should not contain a")
	}
	if !s.Add("a") {
		t.Fatal("Add(a) should report newly added")
	}
	if s.Add("a") {
		t.Fatal("Add(a) again should report already present")
	}
	if !s.Seen("a") {
		t.Fatal("a should be seen")
	}
}

func TestSet_SeenOrAdd(t *testing.T) {
	s := New(2)
	if s.SeenOrAdd("x") {
		t.Fatal("first SeenOrAdd(x) should be false (new)")
	}
	if !s.SeenOrAdd("x") {
		t.Fatal("second SeenOrAdd(x) should be true (seen)")
	}
}

func TestSet_EvictsOldest(t *testing.T) {
	s := New(2)
	s.Add("a")
	s.Add("b")
	s.Add("c") // evicts "a"

	if s.Seen("a") {
		t.Fatal("a should have been evicted")
	}
	if !s.Seen("b") || !s.Seen("c") {
		t.Fatal("b and c should be retained")
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestSet_MinimumCapacity(t *testing.T) {
	s := New(0)
	s.Add("a")
	s.Add("b")
	if s.Seen("a") {
		t.Fatal("capacity should be clamped to 1; a should be gone")
	}
	if !s.Seen("b") {
		t.Fatal("b should be retained")
	}
}
