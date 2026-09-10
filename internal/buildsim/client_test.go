package buildsim

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient points a Client at srv with a short timeout.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: srv.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestHealth_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !got.OK() || got.Status != "ok" {
		t.Fatalf("got %+v, want status ok", got)
	}
}

func TestHealth_Degraded_NoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Health(context.Background())
	if err != nil {
		t.Fatalf("Health returned error for 200/degraded: %v", err)
	}
	if got.OK() {
		t.Fatalf("expected OK()==false for status %q", got.Status)
	}
}

func TestBuilding_OK(t *testing.T) {
	const body = `{"name":"A-Building (LTU)","levels":[` +
		`{"id":"level0","label":"Floor 0"},{"id":"level1","label":"Floor 1"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/building" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Building(context.Background())
	if err != nil {
		t.Fatalf("Building: %v", err)
	}
	if got.Name != "A-Building (LTU)" || len(got.Levels) != 2 {
		t.Fatalf("unexpected building: %+v", got)
	}
	if got.Levels[0].ID != "level0" || got.Levels[0].Label != "Floor 0" {
		t.Fatalf("unexpected first level: %+v", got.Levels[0])
	}
}

func TestBuilding_ToleratesUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"X","levels":[],"addedLater":true}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Building(context.Background()); err != nil {
		t.Fatalf("expected additive field to be tolerated, got %v", err)
	}
}

func TestGetJSON_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Health(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestGetJSON_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status": `)) // truncated
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Health(context.Background())
	if err == nil {
		t.Fatal("expected decode error on truncated body")
	}
}

func TestGetJSON_ContextTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hang until the test lets go
	}))
	defer srv.Close()
	defer close(release)

	c := newTestClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Health(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("call did not abandon promptly: %s", elapsed)
	}
}

func TestClient_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	c, err := New(Config{BaseURL: url, Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("expected transport error against a closed server")
	}
}

func TestNew_TrailingSlashNormalised(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/", Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotPath != "/healthz" {
		t.Fatalf("got path %q, want /healthz (no double slash)", gotPath)
	}
}
