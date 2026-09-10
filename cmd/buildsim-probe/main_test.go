package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRun_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/building":
			_, _ = w.Write([]byte(`{"name":"A","levels":[{"id":"level0","label":"Floor 0"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("BUILDSIM_URL", srv.URL)
	t.Setenv("BUILDSIM_TIMEOUT", "2s")

	if code := run(context.Background(), discardLogger()); code != 0 {
		t.Fatalf("run exit code = %d, want 0", code)
	}
}

func TestRun_MissingConfig(t *testing.T) {
	t.Setenv("BUILDSIM_URL", "")
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
}

func TestRun_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	t.Setenv("BUILDSIM_URL", url)
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
}

func TestRun_Unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"degraded"}`))
	}))
	defer srv.Close()

	t.Setenv("BUILDSIM_URL", srv.URL)
	if code := run(context.Background(), discardLogger()); code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
}
