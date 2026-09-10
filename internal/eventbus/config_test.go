package eventbus

import (
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("EVENTBUS_URL", "")
		t.Setenv("EVENTBUS_TIMEOUT", "")
		t.Setenv("EVENTBUS_IDLE_TIMEOUT", "")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cfg.BaseURL != DefaultURL || cfg.PublishTimeout != DefaultPublishTimeout {
			t.Fatalf("unexpected defaults: %+v", cfg)
		}
	})

	t.Run("overrides and trailing slash", func(t *testing.T) {
		t.Setenv("EVENTBUS_URL", "http://bus:9000/")
		t.Setenv("EVENTBUS_TIMEOUT", "1s")
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cfg.BaseURL != "http://bus:9000" || cfg.PublishTimeout != time.Second {
			t.Fatalf("unexpected: %+v", cfg)
		}
	})

	t.Run("rejects bad url and duration", func(t *testing.T) {
		t.Setenv("EVENTBUS_URL", "ftp://bus")
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("bad scheme should error")
		}
		t.Setenv("EVENTBUS_URL", "http://bus")
		t.Setenv("EVENTBUS_TIMEOUT", "soon")
		if _, err := ConfigFromEnv(); err == nil {
			t.Error("bad duration should error")
		}
	})
}

func TestBrokerConfigFromEnv(t *testing.T) {
	t.Run("defaults, no faults", func(t *testing.T) {
		for _, k := range []string{
			"EVENTBUS_BUFFER", "EVENTBUS_HISTORY", "EVENTBUS_HEARTBEAT",
			"FAULT_DROP_RATE", "FAULT_DUPLICATE", "FAULT_DELAY_MS", "FAULT_REORDER", "FAULT_SEED",
		} {
			t.Setenv(k, "")
		}
		cfg, err := BrokerConfigFromEnv()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cfg.Faults.Enabled() {
			t.Fatalf("faults should be off by default: %+v", cfg.Faults)
		}
	})

	t.Run("parses faults", func(t *testing.T) {
		t.Setenv("FAULT_DROP_RATE", "0.25")
		t.Setenv("FAULT_DUPLICATE", "0.1")
		t.Setenv("FAULT_DELAY_MS", "150")
		t.Setenv("FAULT_REORDER", "true")
		t.Setenv("FAULT_SEED", "99")
		cfg, err := BrokerConfigFromEnv()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		f := cfg.Faults
		if f.DropRate != 0.25 || f.DuplicateRate != 0.1 || f.Delay != 150*time.Millisecond ||
			!f.Reorder || f.Seed != 99 {
			t.Fatalf("parsed faults wrong: %+v", f)
		}
	})

	t.Run("rejects out-of-range drop rate", func(t *testing.T) {
		t.Setenv("FAULT_DROP_RATE", "2")
		if _, err := BrokerConfigFromEnv(); err == nil {
			t.Error("drop rate 2 should error")
		}
	})
}
