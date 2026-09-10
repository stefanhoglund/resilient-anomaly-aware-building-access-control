// Package dedupe provides a bounded set of recently seen identifiers.
//
// The event bus delivers at-least-once, so every consumer must ignore a
// redelivered event. A dedupe.Set remembers the last N ids it was told
// about; the oldest is forgotten when the set is full. It is not safe for
// concurrent use — each consumer drives it from its own single goroutine.
package dedupe

// Set is a fixed-capacity FIFO set of ids.
type Set struct {
	capacity int
	order    []string
	members  map[string]struct{}
}

// New returns a Set that retains the most recent capacity ids. A
// non-positive capacity is treated as 1.
func New(capacity int) *Set {
	if capacity < 1 {
		capacity = 1
	}
	return &Set{
		capacity: capacity,
		members:  make(map[string]struct{}, capacity),
	}
}

// Seen reports whether id is currently in the set.
func (s *Set) Seen(id string) bool {
	_, ok := s.members[id]
	return ok
}

// Add inserts id. It returns true if the id was newly added, false if it
// was already present.
func (s *Set) Add(id string) bool {
	if _, ok := s.members[id]; ok {
		return false
	}
	s.members[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.members, oldest)
	}
	return true
}

// SeenOrAdd is the common check-and-record step: it returns true if the
// id was already seen (leaving the set unchanged), or false if it is new
// (recording it).
func (s *Set) SeenOrAdd(id string) bool {
	return !s.Add(id)
}

// Len reports how many ids are currently retained.
func (s *Set) Len() int { return len(s.order) }
