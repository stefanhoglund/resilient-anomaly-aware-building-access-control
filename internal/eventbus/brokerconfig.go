package eventbus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// BrokerConfigFromEnv reads broker configuration from the environment.
//
//	EVENTBUS_BUFFER       per-subscriber queue depth (int, default 256)
//	EVENTBUS_HISTORY      retained-event count for resume (int, default 1024)
//	EVENTBUS_HEARTBEAT    SSE keep-alive interval (duration, default 15s)
//
//	FAULT_DROP_RATE       [0,1] probability a delivery is dropped (default 0)
//	FAULT_DUPLICATE       [0,1] probability a delivery is duplicated (default 0)
//	FAULT_DELAY_MS        milliseconds added before each delivery (default 0)
//	FAULT_REORDER         "true" to occasionally reorder adjacent events
//	FAULT_SEED            int64 RNG seed for reproducible fault tests (default: time)
//
// The Logger field is not set here; the caller injects it.
func BrokerConfigFromEnv() (BrokerConfig, error) {
	var cfg BrokerConfig
	var err error

	if cfg.Buffer, err = envInt("EVENTBUS_BUFFER", defaultBuffer); err != nil {
		return BrokerConfig{}, err
	}
	if cfg.History, err = envInt("EVENTBUS_HISTORY", defaultHistory); err != nil {
		return BrokerConfig{}, err
	}
	if cfg.Heartbeat, err = envDuration("EVENTBUS_HEARTBEAT", defaultHeartbeat); err != nil {
		return BrokerConfig{}, err
	}

	if cfg.Faults.DropRate, err = envFloat("FAULT_DROP_RATE", 0); err != nil {
		return BrokerConfig{}, err
	}
	if cfg.Faults.DuplicateRate, err = envFloat("FAULT_DUPLICATE", 0); err != nil {
		return BrokerConfig{}, err
	}
	ms, err := envInt("FAULT_DELAY_MS", 0)
	if err != nil {
		return BrokerConfig{}, err
	}
	cfg.Faults.Delay = time.Duration(ms) * time.Millisecond
	cfg.Faults.Reorder = strings.EqualFold(strings.TrimSpace(os.Getenv("FAULT_REORDER")), "true")
	if seed := strings.TrimSpace(os.Getenv("FAULT_SEED")); seed != "" {
		if cfg.Faults.Seed, err = strconv.ParseInt(seed, 10, 64); err != nil {
			return BrokerConfig{}, fmt.Errorf("eventbus: invalid FAULT_SEED %q: %w", seed, err)
		}
	}

	if err := cfg.Faults.Validate(); err != nil {
		return BrokerConfig{}, err
	}
	return cfg, nil
}

func envInt(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("eventbus: invalid %s %q: %w", key, v, err)
	}
	return n, nil
}

func envFloat(key string, def float64) (float64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("eventbus: invalid %s %q: %w", key, v, err)
	}
	return f, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("eventbus: invalid %s %q: %w", key, v, err)
	}
	return d, nil
}
