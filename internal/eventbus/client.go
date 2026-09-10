package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// Client publishes envelopes to the bus and opens subscriptions.
type Client struct {
	cfg Config
	// pub is used for POST /publish and carries a request timeout.
	pub *http.Client
	// stream is used for the long-lived GET /subscribe and must not have
	// an overall timeout; liveness is enforced by IdleTimeout instead.
	stream *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Client{
		cfg: cfg,
		pub: &http.Client{Timeout: cfg.PublishTimeout},
		stream: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
		},
	}, nil
}

// Publish sends one envelope. It returns an error if the envelope is
// invalid or the bus does not accept it (non-202).
func (c *Client) Publish(ctx context.Context, env model.Envelope) error {
	if err := env.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("eventbus: marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/publish", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("eventbus: build publish request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.pub.Do(req)
	if err != nil {
		return fmt.Errorf("eventbus: POST /publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("eventbus: POST /publish: status %s: %s",
			resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// SubscribeOptions controls a subscription.
type SubscribeOptions struct {
	// Types filters the stream to these event types. Empty means all.
	Types []string
	// ResumeFrom, if set, is an envelope id; the bus replays retained
	// events after it before going live. On an automatic reconnect the
	// client always resumes from the last id it saw, regardless of this.
	ResumeFrom string
}

// Subscription is a live stream of envelopes. Read from C until it is
// closed. Close or a cancelled context ends it; after C is closed, Err
// reports why (nil for a clean context cancellation).
type Subscription struct {
	C <-chan model.Envelope

	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.Mutex
	err error
}

// Err returns the terminal error, if any. Call it after C is closed.
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Subscription) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// Close ends the subscription and waits for its goroutine to stop.
func (s *Subscription) Close() {
	s.cancel()
	<-s.done
}

// Subscribe opens a subscription. The first connection attempt is
// synchronous, so an unreachable bus is reported immediately. After that,
// dropped connections are retried with backoff, resuming from the last
// seen id, until the context is cancelled or Close is called.
func (c *Client) Subscribe(ctx context.Context, opts SubscribeOptions) (*Subscription, error) {
	ctx, cancel := context.WithCancel(ctx)

	resp, err := c.open(ctx, opts.Types, opts.ResumeFrom)
	if err != nil {
		cancel()
		return nil, err
	}

	out := make(chan model.Envelope)
	sub := &Subscription{C: out, cancel: cancel, done: make(chan struct{})}

	go c.run(ctx, sub, out, resp, opts)
	return sub, nil
}

const (
	minBackoff = 100 * time.Millisecond
	maxBackoff = 5 * time.Second
)

func (c *Client) run(
	ctx context.Context,
	sub *Subscription,
	out chan<- model.Envelope,
	first *http.Response,
	opts SubscribeOptions,
) {
	defer close(sub.done)
	defer close(out)

	resp := first
	lastID := opts.ResumeFrom
	backoff := minBackoff

	for {
		if resp != nil {
			id, err := c.pump(ctx, resp, out, lastID)
			lastID = id
			resp.Body.Close()
			resp = nil

			if ctx.Err() != nil {
				sub.setErr(nil) // clean shutdown
				return
			}
			if err != nil {
				sub.setErr(err)
			} else {
				backoff = minBackoff
			}
		}

		// Wait, then reconnect resuming from the last id we delivered.
		select {
		case <-ctx.Done():
			sub.setErr(nil)
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}

		r, err := c.open(ctx, opts.Types, lastID)
		if err != nil {
			if ctx.Err() != nil {
				sub.setErr(nil)
				return
			}
			sub.setErr(err)
			continue // back off further and retry
		}
		resp = r
		backoff = minBackoff
	}
}

// pump reads one connection to exhaustion, forwarding envelopes to out.
// It returns the id of the last envelope forwarded (for resume) and the
// error that ended the connection (nil on a clean EOF).
func (c *Client) pump(
	ctx context.Context,
	resp *http.Response,
	out chan<- model.Envelope,
	lastID string,
) (string, error) {
	dec := newSSEDecoder(resp.Body)

	// Idle watchdog: closing the body unblocks the blocked Read below.
	watchdog := time.AfterFunc(c.cfg.IdleTimeout, func() { resp.Body.Close() })
	defer watchdog.Stop()

	for {
		ev, err := dec.next()
		if err != nil {
			if ctx.Err() != nil {
				return lastID, nil
			}
			if err == io.EOF {
				return lastID, nil
			}
			return lastID, fmt.Errorf("eventbus: read stream: %w", err)
		}
		watchdog.Reset(c.cfg.IdleTimeout)

		if len(ev.data) == 0 {
			continue // heartbeat
		}
		var env model.Envelope
		if err := json.Unmarshal(ev.data, &env); err != nil {
			// A frame we cannot parse is logged by the caller's design;
			// skip it rather than tear down the stream.
			continue
		}
		select {
		case out <- env:
			lastID = env.ID
		case <-ctx.Done():
			return lastID, nil
		}
	}
}

func (c *Client) open(ctx context.Context, types []string, resumeFrom string) (*http.Response, error) {
	u := c.cfg.BaseURL + "/subscribe"
	if len(types) > 0 {
		u += "?types=" + strings.Join(types, ",")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("eventbus: build subscribe request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if resumeFrom != "" {
		req.Header.Set("Last-Event-ID", resumeFrom)
	}

	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eventbus: GET /subscribe: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("eventbus: GET /subscribe: status %s: %s",
			resp.Status, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}
