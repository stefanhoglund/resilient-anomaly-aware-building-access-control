package buildsim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxErrorBody caps how much of a non-2xx response body we read into an
// error message, so a misbehaving server cannot make us allocate freely.
const maxErrorBody = 4 << 10 // 4 KiB

// userAgent identifies this client in BuildSim's logs.
const userAgent = "buildsim-client/0.1"

// ErrUnhealthy is not returned by this package; callers that want to
// treat a non-ok /healthz as an error can wrap Health.OK() with it.
var ErrUnhealthy = errors.New("buildsim: reported unhealthy")

// Client is a reusable, concurrency-safe BuildSim HTTP client.
//
// It is the single place that knows BuildSim's URL layout, status-code
// contract and JSON shapes. Construct it once per process and share it.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client for the given configuration.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Client{
		baseURL: cfg.BaseURL,
		http:    &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// Health calls GET /healthz. A transport error, non-2xx status or
// undecodable body is returned as an error. A 2xx response with
// status != "ok" is returned without error; inspect Health.OK().
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	if err := c.getJSON(ctx, "/healthz", &h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// Building calls GET /api/building.
func (c *Client) Building(ctx context.Context) (Building, error) {
	var b Building
	if err := c.getJSON(ctx, "/api/building", &b); err != nil {
		return Building{}, err
	}
	return b, nil
}

// getJSON performs GET baseURL+path and decodes a JSON body into dst.
// Every failure mode is wrapped with the path for context.
func (c *Client) getJSON(ctx context.Context, path string, dst any) error {
	endpoint := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("buildsim: build request for %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		// Covers connection refused, DNS failure, TLS errors and
		// context deadline/cancellation.
		return fmt.Errorf("buildsim: GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("buildsim: GET %s: unexpected status %s: %s",
			path, resp.Status, strings.TrimSpace(string(snippet)))
	}

	// Lenient decode: tolerate fields BuildSim may add later. The strict
	// shape is asserted by the contract test, not enforced at runtime.
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		// We still refuse a partial or foreign payload rather than
		// returning a half-populated value.
		return fmt.Errorf("buildsim: GET %s: decode body: %w", path, err)
	}
	return nil
}
