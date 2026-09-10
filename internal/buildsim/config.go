// Package buildsim is the only component that knows BuildSim's HTTP API.
// It converts BuildSim's endpoints into typed Go values and typed errors;
// nothing else in the system reaches BuildSim over HTTP directly.
package buildsim

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultTimeout is used when BUILDSIM_TIMEOUT is not set.
const DefaultTimeout = 5 * time.Second

// Config is everything the client needs to reach BuildSim.
type Config struct {
	// BaseURL is the BuildSim origin, e.g. "http://127.0.0.1:9090".
	// A trailing slash is allowed and ignored.
	BaseURL string
	// Timeout bounds a single call, applied both to the underlying
	// http.Client and, per call, through the request context.
	Timeout time.Duration
}

// ConfigFromEnv reads configuration from the process environment.
//
//	BUILDSIM_URL      required, must be an absolute http(s) URL
//	BUILDSIM_TIMEOUT  optional Go duration (e.g. "3s"), default DefaultTimeout
//
// A missing or malformed value is a fatal configuration error and is
// returned rather than defaulted, so a misconfigured deployment fails
// fast at startup.
func ConfigFromEnv() (Config, error) {
	raw := strings.TrimSpace(os.Getenv("BUILDSIM_URL"))
	if raw == "" {
		return Config{}, fmt.Errorf("buildsim: BUILDSIM_URL is required")
	}

	cfg := Config{BaseURL: raw, Timeout: DefaultTimeout}

	if t := strings.TrimSpace(os.Getenv("BUILDSIM_TIMEOUT")); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return Config{}, fmt.Errorf("buildsim: invalid BUILDSIM_TIMEOUT %q: %w", t, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("buildsim: BUILDSIM_TIMEOUT must be positive, got %s", d)
		}
		cfg.Timeout = d
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks the config is usable and normalises BaseURL.
func (c *Config) validate() error {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("buildsim: invalid BUILDSIM_URL %q: %w", c.BaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("buildsim: BUILDSIM_URL must be http or https, got %q", c.BaseURL)
	}
	if u.Host == "" {
		return fmt.Errorf("buildsim: BUILDSIM_URL has no host: %q", c.BaseURL)
	}

	// Normalise: keep scheme://host[/path], drop trailing slash and any
	// query or fragment so path joining in the client is predictable.
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	c.BaseURL = u.String()
	return nil
}
