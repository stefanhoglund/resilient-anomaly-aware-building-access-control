package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRun_StartsAndStopsGracefully(t *testing.T) {
	t.Setenv("EVENTBUS_ADDR", "127.0.0.1:0") // ephemeral port
	t.Setenv("FAULT_DROP_RATE", "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, discardLogger()) }()

	// Let it bind, then ask it to stop.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not return after context cancellation")
	}
}

func TestRun_RejectsBadFaultConfig(t *testing.T) {
	t.Setenv("EVENTBUS_ADDR", "127.0.0.1:0")
	t.Setenv("FAULT_DROP_RATE", "3.0")

	code := run(context.Background(), discardLogger())
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for invalid config", code)
	}
}

func TestRun_PortInUse(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot open a listener: %v", err)
	}
	defer l.Close()

	t.Setenv("EVENTBUS_ADDR", l.Addr().String())
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("exit code = %d, want 1 when the port is taken", code)
	}
}
