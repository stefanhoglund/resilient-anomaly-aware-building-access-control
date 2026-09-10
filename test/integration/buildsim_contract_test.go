//go:build integration

// Package integration holds tests that run against real external
// dependencies. They are excluded from the default build and require the
// `integration` build tag:
//
//	go test -tags=integration ./test/integration/...
//
// This file is the BuildSim contract test: it asserts that the live
// BuildSim instance still matches the schema internal/buildsim assumes.
// A failure here while the hermetic unit tests pass means BuildSim's API
// has drifted.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/buildsim"
)

func newClient(t *testing.T) *buildsim.Client {
	t.Helper()
	url := os.Getenv("BUILDSIM_URL")
	if url == "" {
		t.Skip("BUILDSIM_URL not set; skipping BuildSim contract test")
	}
	c, err := buildsim.New(buildsim.Config{BaseURL: url, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestContract_Health(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := newClient(t).Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.OK() {
		t.Fatalf("BuildSim not healthy: status=%q", h.Status)
	}
}

func TestContract_Building(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	b, err := newClient(t).Building(ctx)
	if err != nil {
		t.Fatalf("Building: %v", err)
	}
	if b.Name == "" {
		t.Error("building name is empty")
	}
	if len(b.Levels) == 0 {
		t.Fatal("building has no levels")
	}
	for i, l := range b.Levels {
		if l.ID == "" || l.Label == "" {
			t.Errorf("level %d incomplete: %+v", i, l)
		}
	}
}
