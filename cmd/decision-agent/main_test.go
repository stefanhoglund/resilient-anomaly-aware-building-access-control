package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/eventbus"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRun_BadEventBusURL(t *testing.T) {
	t.Setenv("EVENTBUS_URL", "ftp://nope")
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestRun_MinimalLoopDecisionLeg(t *testing.T) {
	bus := httptest.NewServer(eventbus.NewBroker(eventbus.BrokerConfig{
		Logger:    discardLogger(),
		Heartbeat: 50 * time.Millisecond,
	}))
	defer bus.Close()

	t.Setenv("EVENTBUS_URL", bus.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, discardLogger()) }()
	defer func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("run exit = %d, want 0", code)
			}
		case <-time.After(3 * time.Second):
			t.Error("run did not stop after cancel")
		}
	}()

	client, err := eventbus.New(eventbus.Config{
		BaseURL: bus.URL, PublishTimeout: 2 * time.Second, IdleTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	sub, err := client.Subscribe(ctx, eventbus.SubscribeOptions{
		Types: []string{model.TypeAccessDecision, model.TypeDoorCommand},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(200 * time.Millisecond) // let the agent subscribe

	// The injected anomaly: staff card at the restricted lab, 22:00.
	badge, err := model.NewEnvelope("simulator", model.TypeBadgeRead,
		time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC),
		model.BadgeRead{ReaderID: model.Reader1, DoorID: model.Door1, RoomID: model.RoomLab, CardID: model.CardStaff})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if err := client.Publish(ctx, badge); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case env, ok := <-sub.C:
		if !ok {
			t.Fatalf("subscription closed: %v", sub.Err())
		}
		if env.Type != model.TypeAccessDecision {
			t.Fatalf("first event = %s, want access.decision", env.Type)
		}
		d, err := model.Decode[model.AccessDecision](env)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if d.Action != model.ActionAlert {
			t.Fatalf("action = %q, want alert", d.Action)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no access.decision published")
	}
}
