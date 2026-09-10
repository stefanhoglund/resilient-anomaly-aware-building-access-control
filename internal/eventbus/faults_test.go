package eventbus

import (
	"testing"
	"time"
)

func TestFaultConfig_EnabledAndValidate(t *testing.T) {
	if (FaultConfig{}).Enabled() {
		t.Error("zero FaultConfig should be disabled")
	}
	if !(FaultConfig{DropRate: 0.1}).Enabled() {
		t.Error("drop rate should enable faults")
	}
	if !(FaultConfig{Delay: time.Millisecond}).Enabled() {
		t.Error("delay should enable faults")
	}

	if err := (FaultConfig{DropRate: 1.5}).Validate(); err == nil {
		t.Error("drop rate > 1 should be invalid")
	}
	if err := (FaultConfig{DuplicateRate: -0.1}).Validate(); err == nil {
		t.Error("negative duplicate rate should be invalid")
	}
	if err := (FaultConfig{DropRate: 0.5, DuplicateRate: 0.5}).Validate(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestFaults_DeterministicWithSeed(t *testing.T) {
	cfg := FaultConfig{DropRate: 0.5, Seed: 42}
	seq := func() []bool {
		f := newFaults(cfg)
		out := make([]bool, 20)
		for i := range out {
			out[i] = f.drop()
		}
		return out
	}
	a, b := seq(), seq()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("seeded fault RNG not reproducible at %d", i)
		}
	}
}

func TestFaults_RatesRoughlyHold(t *testing.T) {
	f := newFaults(FaultConfig{DropRate: 0.3, Seed: 1})
	dropped := 0
	const n = 10000
	for i := 0; i < n; i++ {
		if f.drop() {
			dropped++
		}
	}
	got := float64(dropped) / n
	if got < 0.27 || got > 0.33 {
		t.Fatalf("drop rate = %.3f, want ~0.30", got)
	}
}

func TestFaults_ZeroRatesNeverFire(t *testing.T) {
	f := newFaults(FaultConfig{Seed: 7})
	for i := 0; i < 1000; i++ {
		if f.drop() || f.duplicate() || f.holdForReorder() {
			t.Fatal("a zero-configured fault fired")
		}
	}
}
