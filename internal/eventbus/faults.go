package eventbus

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// FaultConfig configures the message faults the broker can inject on the
// delivery path, for the system-level tests the project requires
// (delayed, dropped, duplicated, reordered messages). All zero values
// mean "no fault"; the bus behaves normally.
type FaultConfig struct {
	// DropRate is the probability [0,1] that a given delivery is dropped.
	DropRate float64
	// DuplicateRate is the probability [0,1] that a delivered message is
	// sent a second time immediately.
	DuplicateRate float64
	// Delay is added before each delivery (models a slow link).
	Delay time.Duration
	// Reorder, when true, occasionally holds one message back and emits
	// it after the following one, so consumers see out-of-order arrival.
	Reorder bool
	// Seed fixes the RNG for reproducible tests. Zero uses a time seed.
	Seed int64
}

// Enabled reports whether any fault is configured.
func (c FaultConfig) Enabled() bool {
	return c.DropRate > 0 || c.DuplicateRate > 0 || c.Delay > 0 || c.Reorder
}

// Validate rejects nonsensical configuration.
func (c FaultConfig) Validate() error {
	if c.DropRate < 0 || c.DropRate > 1 {
		return fmt.Errorf("eventbus: FAULT_DROP_RATE %g out of [0,1]", c.DropRate)
	}
	if c.DuplicateRate < 0 || c.DuplicateRate > 1 {
		return fmt.Errorf("eventbus: FAULT_DUPLICATE %g out of [0,1]", c.DuplicateRate)
	}
	if c.Delay < 0 {
		return fmt.Errorf("eventbus: FAULT_DELAY_MS is negative")
	}
	return nil
}

// probability of holding a message for reordering, when Reorder is on.
const reorderHoldChance = 0.5

// faults makes the random decisions for one broker. Its RNG is guarded by
// a mutex so concurrent subscriber goroutines share one reproducible
// sequence when Seed is set.
type faults struct {
	cfg FaultConfig
	mu  sync.Mutex
	rng *rand.Rand
}

func newFaults(cfg FaultConfig) *faults {
	seed := cfg.Seed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &faults{cfg: cfg, rng: rand.New(rand.NewSource(seed))}
}

func (f *faults) roll() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rng.Float64()
}

func (f *faults) drop() bool {
	return f.cfg.DropRate > 0 && f.roll() < f.cfg.DropRate
}

func (f *faults) duplicate() bool {
	return f.cfg.DuplicateRate > 0 && f.roll() < f.cfg.DuplicateRate
}

func (f *faults) holdForReorder() bool {
	return f.cfg.Reorder && f.roll() < reorderHoldChance
}

func (f *faults) delay() time.Duration {
	return f.cfg.Delay
}
