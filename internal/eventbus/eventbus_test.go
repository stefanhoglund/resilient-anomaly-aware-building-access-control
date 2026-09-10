package eventbus

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testBus starts a broker on an httptest server and returns a connected
// client plus the server (for CloseClientConnections in reconnect tests).
func testBus(t *testing.T, cfg BrokerConfig) (*Client, *httptest.Server) {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = 50 * time.Millisecond
	}
	srv := httptest.NewServer(NewBroker(cfg))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		BaseURL:        srv.URL,
		PublishTimeout: 2 * time.Second,
		IdleTimeout:    500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	return c, srv
}

func mustEnvelope(t *testing.T, typ string, payload any) model.Envelope {
	t.Helper()
	e, err := model.NewEnvelope("test", typ, time.Now(), payload)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	return e
}

// recvWithin reads one envelope from the subscription or fails.
func recvWithin(t *testing.T, sub *Subscription, d time.Duration) model.Envelope {
	t.Helper()
	select {
	case env, ok := <-sub.C:
		if !ok {
			t.Fatalf("subscription closed early: %v", sub.Err())
		}
		return env
	case <-time.After(d):
		t.Fatalf("timed out waiting for an envelope")
		return model.Envelope{}
	}
}

func TestBus_PublishSubscribe(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	// Give the subscription a moment to register before publishing.
	time.Sleep(50 * time.Millisecond)

	want := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{CardID: model.CardStaff})
	if err := c.Publish(ctx, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := recvWithin(t, sub, time.Second)
	if got.ID != want.ID || got.Type != want.Type {
		t.Fatalf("got %+v, want id %s type %s", got, want.ID, want.Type)
	}
	br, err := model.Decode[model.BadgeRead](got)
	if err != nil || br.CardID != model.CardStaff {
		t.Fatalf("payload round-trip failed: %+v %v", br, err)
	}
}

func TestBus_TypeFilter(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{Types: []string{model.TypeDoorState}})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	_ = c.Publish(ctx, mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{}))
	want := mustEnvelope(t, model.TypeDoorState, model.DoorState{DoorID: model.Door1})
	_ = c.Publish(ctx, want)

	got := recvWithin(t, sub, time.Second)
	if got.ID != want.ID {
		t.Fatalf("filter delivered wrong event: %s", got.Type)
	}
}

func TestBus_Resume(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{History: 10})
	ctx := context.Background()

	var ids []string
	for i := 0; i < 3; i++ {
		e := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})
		if err := c.Publish(ctx, e); err != nil {
			t.Fatalf("publish: %v", err)
		}
		ids = append(ids, e.ID)
		time.Sleep(2 * time.Millisecond)
	}

	sub, err := c.Subscribe(ctx, SubscribeOptions{ResumeFrom: ids[0]})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	// Should replay events after ids[0]: ids[1], ids[2].
	for _, want := range ids[1:] {
		got := recvWithin(t, sub, time.Second)
		if got.ID != want {
			t.Fatalf("resume delivered %s, want %s", got.ID, want)
		}
	}
}

func TestBus_ReconnectResumesFromLastID(t *testing.T) {
	c, srv := testBus(t, BrokerConfig{History: 50})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	first := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})
	_ = c.Publish(ctx, first)
	if got := recvWithin(t, sub, time.Second); got.ID != first.ID {
		t.Fatalf("got %s, want %s", got.ID, first.ID)
	}

	// Sever the TCP connection; the client must reconnect and resume.
	srv.CloseClientConnections()
	time.Sleep(50 * time.Millisecond)

	var after []string
	for i := 0; i < 3; i++ {
		e := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})
		_ = c.Publish(ctx, e)
		after = append(after, e.ID)
		time.Sleep(2 * time.Millisecond)
	}

	got := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(got) < len(after) {
		select {
		case env, ok := <-sub.C:
			if !ok {
				t.Fatalf("subscription closed: %v", sub.Err())
			}
			got[env.ID] = true
		case <-deadline:
			t.Fatalf("after reconnect got %d/%d events", len(got), len(after))
		}
	}
	for _, id := range after {
		if !got[id] {
			t.Fatalf("missing event %s after reconnect", id)
		}
	}
}

func TestBus_Fault_DropAll(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{Faults: FaultConfig{DropRate: 1, Seed: 1}})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	if err := c.Publish(ctx, mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})); err != nil {
		t.Fatalf("publish should still succeed: %v", err)
	}
	// Nothing should arrive; but the subscription stays open, so use a
	// bare timeout rather than expectNothing (which also accepts close).
	select {
	case env := <-sub.C:
		t.Fatalf("dropped-all bus delivered %s", env.ID)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestBus_Fault_DuplicateAll(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{Faults: FaultConfig{DuplicateRate: 1, Seed: 1}})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	want := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})
	_ = c.Publish(ctx, want)

	a := recvWithin(t, sub, time.Second)
	b := recvWithin(t, sub, time.Second)
	if a.ID != want.ID || b.ID != want.ID {
		t.Fatalf("expected the same event twice, got %s and %s", a.ID, b.ID)
	}
}

func TestBus_Fault_Delay(t *testing.T) {
	delay := 250 * time.Millisecond
	c, _ := testBus(t, BrokerConfig{Faults: FaultConfig{Delay: delay}})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	_ = c.Publish(ctx, mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{}))
	recvWithin(t, sub, 2*time.Second)
	if elapsed := time.Since(start); elapsed < delay {
		t.Fatalf("event arrived in %s, expected at least %s", elapsed, delay)
	}
}

func TestBus_Fault_Reorder(t *testing.T) {
	c, _ := testBus(t, BrokerConfig{Faults: FaultConfig{Reorder: true, Seed: 1}})
	ctx := context.Background()

	sub, err := c.Subscribe(ctx, SubscribeOptions{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	time.Sleep(50 * time.Millisecond)

	const n = 40
	sent := make([]string, n)
	for i := 0; i < n; i++ {
		e := mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{})
		sent[i] = e.ID
		_ = c.Publish(ctx, e)
		time.Sleep(time.Millisecond)
	}

	recv := make([]string, 0, n)
	for len(recv) < n {
		recv = append(recv, recvWithin(t, sub, 2*time.Second).ID)
	}

	// Same multiset, but not the same order.
	if !sameMultiset(sent, recv) {
		t.Fatalf("reorder changed the set of events")
	}
	if equal(sent, recv) {
		t.Fatalf("reorder fault produced identical order across %d events", n)
	}
}

// White-box: a subscriber that never drains its queue must not stall
// publish, and its overflow must be counted as lag.
func TestBroker_PublishNeverBlocksOnSlowSubscriber(t *testing.T) {
	b := NewBroker(BrokerConfig{Buffer: 2, Logger: quietLogger()})
	sub := &subscriber{ch: make(chan model.Envelope, 2)}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			b.publish(mustEnvelope(t, model.TypeBadgeRead, model.BadgeRead{}))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a subscriber that never reads")
	}

	if len(sub.ch) != cap(sub.ch) {
		t.Fatalf("subscriber queue holds %d, want %d", len(sub.ch), cap(sub.ch))
	}
	if b.lagged.Load() == 0 {
		t.Fatal("expected lag to be counted")
	}
	if b.published.Load() != 50 {
		t.Fatalf("published counter = %d, want 50", b.published.Load())
	}
}

func TestBroker_RejectsInvalidEnvelope(t *testing.T) {
	srv := httptest.NewServer(NewBroker(BrokerConfig{Logger: quietLogger()}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/publish", "application/json", strings.NewReader(`{"id":"nope"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestBroker_Health(t *testing.T) {
	srv := httptest.NewServer(NewBroker(BrokerConfig{Logger: quietLogger()}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// helpers

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
