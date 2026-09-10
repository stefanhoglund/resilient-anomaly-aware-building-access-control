package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/eventbus"
	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// minimal in-memory BuildSim equipment API for the wiring test.
func fakeBuildSim() http.Handler {
	var (
		mu    sync.Mutex
		store = map[string]map[string]any{}
		ver   int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/equipment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "no", 405)
			return
		}
		var eq map[string]any
		_ = json.NewDecoder(r.Body).Decode(&eq)
		id, _ := eq["id"].(string)
		mu.Lock()
		defer mu.Unlock()
		if _, ok := store[id]; ok {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"exists"}`))
			return
		}
		ver++
		eq["version"] = ver
		store[id] = eq
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(eq)
	})
	mux.HandleFunc("/api/equipment/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/equipment/")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			eq, ok := store[id]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(eq)
		case http.MethodPut:
			if _, ok := store[id]; !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			var eq map[string]any
			_ = json.NewDecoder(r.Body).Decode(&eq)
			ver++
			eq["id"] = id
			eq["version"] = ver
			store[id] = eq
			_ = json.NewEncoder(w).Encode(eq)
		}
	})
	return mux
}

func lockStateInBuildSim(t *testing.T, base string) model.LockState {
	t.Helper()
	resp, err := http.Get(base + "/api/equipment/" + model.Door1)
	if err != nil {
		t.Fatalf("get equipment: %v", err)
	}
	defer resp.Body.Close()
	var eq struct {
		Actuators []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"actuators"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&eq)
	for _, a := range eq.Actuators {
		if a.ID == "lock" {
			return model.LockState(a.State)
		}
	}
	return ""
}

func TestRun_MissingBuildSimURL(t *testing.T) {
	t.Setenv("BUILDSIM_URL", "")
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestRun_MinimalLoopActuatorLeg(t *testing.T) {
	bsim := httptest.NewServer(fakeBuildSim())
	defer bsim.Close()
	bus := httptest.NewServer(eventbus.NewBroker(eventbus.BrokerConfig{
		Logger:    discardLogger(),
		Heartbeat: 50 * time.Millisecond,
	}))
	defer bus.Close()

	t.Setenv("BUILDSIM_URL", bsim.URL)
	t.Setenv("EVENTBUS_URL", bus.URL)
	t.Setenv("DOOR_ID", model.Door1)

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
		t.Fatalf("bus client: %v", err)
	}

	sub, err := client.Subscribe(ctx, eventbus.SubscribeOptions{Types: []string{model.TypeDoorState}})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	// Give the actuator time to subscribe.
	time.Sleep(200 * time.Millisecond)

	command, err := model.NewEnvelope("test", model.TypeDoorCommand, time.Now(), model.DoorCommand{
		CommandID:   "cmd-1",
		DecisionID:  "dec-1",
		DoorID:      model.Door1,
		DesiredLock: model.LockUnlocked,
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if err := client.Publish(ctx, command); err != nil {
		t.Fatalf("publish command: %v", err)
	}

	select {
	case env, ok := <-sub.C:
		if !ok {
			t.Fatalf("door.state subscription closed: %v", sub.Err())
		}
		ds, err := model.Decode[model.DoorState](env)
		if err != nil {
			t.Fatalf("decode door.state: %v", err)
		}
		if !ds.Applied || ds.Lock != model.LockUnlocked {
			t.Fatalf("door.state = %+v, want applied unlocked", ds)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no door.state published")
	}

	if got := lockStateInBuildSim(t, bsim.URL); got != model.LockUnlocked {
		t.Fatalf("BuildSim lock = %q, want unlocked", got)
	}
}
