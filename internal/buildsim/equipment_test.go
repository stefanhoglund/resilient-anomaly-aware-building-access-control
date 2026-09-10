package buildsim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBuildSim is a minimal in-memory stand-in for BuildSim's equipment
// API, matching the behaviour recorded in ADR 0002.
type fakeBuildSim struct {
	mu      sync.Mutex
	store   map[string]Equipment
	version int
}

func newFakeBuildSim() *fakeBuildSim {
	return &fakeBuildSim{store: map[string]Equipment{}}
}

func (f *fakeBuildSim) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/equipment", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			f.mu.Lock()
			defer f.mu.Unlock()
			list := make([]Equipment, 0, len(f.store))
			for _, e := range f.store {
				list = append(list, e)
			}
			writeJSON(w, 200, list)
		case http.MethodPost:
			var eq Equipment
			if err := json.NewDecoder(r.Body).Decode(&eq); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad json"})
				return
			}
			if eq.ID == "" || eq.Level == "" || eq.Room == "" {
				writeJSON(w, 400, map[string]string{"error": "level is required"})
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, ok := f.store[eq.ID]; ok {
				writeJSON(w, 409, map[string]string{"error": "equipment already exists"})
				return
			}
			f.version++
			eq.Version = f.version
			stamp(&eq)
			f.store[eq.ID] = eq
			writeJSON(w, 201, eq)
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/api/equipment/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/equipment/")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			eq, ok := f.store[id]
			if !ok {
				writeJSON(w, 404, map[string]string{"error": "equipment not found"})
				return
			}
			writeJSON(w, 200, eq)
		case http.MethodPut:
			_, exists := f.store[id]
			if !exists {
				writeJSON(w, 404, map[string]string{"error": "equipment not found"})
				return
			}
			var eq Equipment
			if err := json.NewDecoder(r.Body).Decode(&eq); err != nil {
				writeJSON(w, 400, map[string]string{"error": "bad json"})
				return
			}
			if eq.Level == "" || eq.Room == "" {
				writeJSON(w, 400, map[string]string{"error": "level is required"})
				return
			}
			f.version++
			eq.ID = id
			eq.Version = f.version
			stamp(&eq)
			f.store[id] = eq
			writeJSON(w, 200, eq)
		case http.MethodDelete:
			if _, ok := f.store[id]; !ok {
				writeJSON(w, 404, map[string]string{"error": "not found"})
				return
			}
			delete(f.store, id)
			w.WriteHeader(200)
		default:
			w.WriteHeader(405)
		}
	})
	return mux
}

func stamp(eq *Equipment) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range eq.Sensors {
		eq.Sensors[i].Timestamp = now
	}
	for i := range eq.Actuators {
		eq.Actuators[i].Timestamp = now
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fakeClient(t *testing.T) (*Client, *fakeBuildSim) {
	t.Helper()
	fb := newFakeBuildSim()
	srv := httptest.NewServer(fb.handler())
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, fb
}

func doorEquipment() Equipment {
	return Equipment{
		ID: "door-1", Name: "Door 1", Type: "door", Level: "level0", Room: "A1007",
		Sensors: []Sensor{
			{ID: "contact", Name: "Contact", DataType: DataTypeBinary},
			{ID: "badge", Name: "Badge", DataType: DataTypeText},
		},
		Actuators: []Actuator{{ID: "lock", Name: "Lock", State: "locked"}},
	}
}

func TestCreateEquipment(t *testing.T) {
	c, _ := fakeClient(t)
	got, err := c.CreateEquipment(context.Background(), doorEquipment())
	if err != nil {
		t.Fatalf("CreateEquipment: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if s, ok := got.Sensor("contact"); !ok || s.Timestamp == "" {
		t.Errorf("contact sensor missing or unstamped: %+v", s)
	}
}

func TestCreateEquipment_Conflict(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()
	if _, err := c.CreateEquipment(ctx, doorEquipment()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := c.CreateEquipment(ctx, doorEquipment())
	if !errors.Is(err, ErrEquipmentExists) {
		t.Fatalf("second create err = %v, want ErrEquipmentExists", err)
	}
}

func TestGetEquipment_NotFound(t *testing.T) {
	c, _ := fakeClient(t)
	_, err := c.GetEquipment(context.Background(), "nope")
	if !errors.Is(err, ErrEquipmentNotFound) {
		t.Fatalf("err = %v, want ErrEquipmentNotFound", err)
	}
}

func TestEnsureEquipment_CreatesThenLeavesAlone(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()

	first, err := c.EnsureEquipment(ctx, doorEquipment())
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	if first.Version != 1 {
		t.Fatalf("version = %d, want 1", first.Version)
	}

	// Change the lock, then Ensure again: it must NOT overwrite.
	changed := doorEquipment()
	changed.Actuators[0].State = "unlocked"
	if _, err := c.PutEquipment(ctx, changed); err != nil {
		t.Fatalf("put: %v", err)
	}

	second, err := c.EnsureEquipment(ctx, doorEquipment())
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if a, _ := second.Actuator("lock"); a.State != "unlocked" {
		t.Fatalf("EnsureEquipment clobbered an existing value: lock = %q", a.State)
	}
}

func TestPutEquipment_BumpsVersion(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()
	if _, err := c.CreateEquipment(ctx, doorEquipment()); err != nil {
		t.Fatalf("create: %v", err)
	}

	upd := doorEquipment()
	upd.Actuators[0].State = "unlocked"
	upd.Sensors[0].BinaryValue = true

	got, err := c.PutEquipment(ctx, upd)
	if err != nil {
		t.Fatalf("PutEquipment: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	if a, _ := got.Actuator("lock"); a.State != "unlocked" {
		t.Errorf("lock state = %q, want unlocked", a.State)
	}
	if s, _ := got.Sensor("contact"); !s.BinaryValue {
		t.Errorf("contact binary_value = false, want true")
	}
}

func TestPutEquipment_MissingIsNotFound(t *testing.T) {
	c, _ := fakeClient(t)
	_, err := c.PutEquipment(context.Background(), doorEquipment())
	if !errors.Is(err, ErrEquipmentNotFound) {
		t.Fatalf("err = %v, want ErrEquipmentNotFound", err)
	}
}

func TestWriteValidation(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()

	bad := doorEquipment()
	bad.Level = ""
	if _, err := c.CreateEquipment(ctx, bad); err == nil || !strings.Contains(err.Error(), "level") {
		t.Fatalf("expected local level validation error, got %v", err)
	}

	bad = doorEquipment()
	bad.Sensors[0].DataType = "numeric"
	if _, err := c.CreateEquipment(ctx, bad); err == nil || !strings.Contains(err.Error(), "data_type") {
		t.Fatalf("expected data_type validation error, got %v", err)
	}
}

func TestDeleteEquipment_Idempotent(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()
	if _, err := c.CreateEquipment(ctx, doorEquipment()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.DeleteEquipment(ctx, "door-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := c.DeleteEquipment(ctx, "door-1"); err != nil {
		t.Fatalf("second delete should be a no-op, got %v", err)
	}
}

func TestListEquipment(t *testing.T) {
	c, _ := fakeClient(t)
	ctx := context.Background()
	_, _ = c.CreateEquipment(ctx, doorEquipment())

	list, err := c.ListEquipment(ctx)
	if err != nil {
		t.Fatalf("ListEquipment: %v", err)
	}
	if len(list) != 1 || list[0].ID != "door-1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestEquipment_ContextTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer slow.Close()
	c, _ := New(Config{BaseURL: slow.URL, Timeout: time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := c.GetEquipment(ctx, "door-1")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
