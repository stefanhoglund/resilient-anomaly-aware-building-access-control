package dooractuator

// seenSet remembers a bounded number of recently processed command ids so
// a redelivered command is applied only once. It is a plain FIFO: the
// oldest id is forgotten when the set is full. Not safe for concurrent
// use (the actuator is single-goroutine).
type seenSet struct {
	capacity int
	order    []string
	set      map[string]struct{}
}

func newSeenSet(capacity int) *seenSet {
	return &seenSet{
		capacity: capacity,
		set:      make(map[string]struct{}, capacity),
	}
}

func (s *seenSet) has(id string) bool {
	_, ok := s.set[id]
	return ok
}

func (s *seenSet) add(id string) {
	if _, ok := s.set[id]; ok {
		return
	}
	s.set[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.set, oldest)
	}
}
