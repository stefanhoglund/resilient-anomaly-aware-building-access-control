package eventbus

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client configuration.
const (
	// DefaultURL is where cmd/eventbus listens by default.
	DefaultURL = "http://127.0.0.1:8080"
	// DefaultPublishTimeout bounds a single POST /publish call.
	DefaultPublishTimeout = 5 * time.Second
	// DefaultIdleTimeout: if a subscription receives nothing (not even a
	// heartbeat) for this long, it reconnects.
	DefaultIdleTimeout = 45 * time.Second
)

// Config configures a Client.
type Config struct {
	// BaseURL is the bus origin, e.g. "http://127.0.0.1:8080".
	BaseURL string
	// PublishTimeout bounds one publish call.
	PublishTimeout time.Duration
	// IdleTimeout triggers a reconnect when a subscription goes quiet.
	IdleTimeout time.Duration
}

// ConfigFromEnv reads client configuration from the environment:
//
//	EVENTBUS_URL           default http://127.0.0.1:8080
//	EVENTBUS_TIMEOUT       publish timeout, Go duration, default 5s
//	EVENTBUS_IDLE_TIMEOUT  subscription idle timeout, default 45s
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		BaseURL:        DefaultURL,
		PublishTimeout: DefaultPublishTimeout,
		IdleTimeout:    DefaultIdleTimeout,
	}
	if v := strings.TrimSpace(os.Getenv("EVENTBUS_URL")); v != "" {
		cfg.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("EVENTBUS_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("eventbus: invalid EVENTBUS_TIMEOUT %q: %w", v, err)
		}
		cfg.PublishTimeout = d
	}
	if v := strings.TrimSpace(os.Getenv("EVENTBUS_IDLE_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("eventbus: invalid EVENTBUS_IDLE_TIMEOUT %q: %w", v, err)
		}
		cfg.IdleTimeout = d
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.PublishTimeout <= 0 {
		c.PublishTimeout = DefaultPublishTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("eventbus: invalid EVENTBUS_URL %q: %w", c.BaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("eventbus: EVENTBUS_URL must be http or https, got %q", c.BaseURL)
	}
	if u.Host == "" {
		return fmt.Errorf("eventbus: EVENTBUS_URL has no host: %q", c.BaseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	c.BaseURL = u.String()
	return nil
}
