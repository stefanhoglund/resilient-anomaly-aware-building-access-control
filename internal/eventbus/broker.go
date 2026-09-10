package eventbus

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"
)

// Broker defaults.
const (
	defaultBuffer    = 256
	defaultHistory   = 1024
	defaultHeartbeat = 15 * time.Second
	maxPublishBytes  = 1 << 20 // 1 MiB
	// reorderMaxHold bounds how long the reorder fault delays a held-back
	// event when no following event arrives to trigger its release.
	reorderMaxHold = 500 * time.Millisecond
)

// BrokerConfig configures a Broker. Zero fields take the defaults above.
type BrokerConfig struct {
	// Buffer is the per-subscriber queue depth. When a subscriber falls
	// this far behind, the broker drops its oldest queued message and
	// counts it as lag.
	Buffer int
	// History is how many recent envelopes are retained for resume.
	History int
	// Heartbeat is the interval between SSE keep-alive comments.
	Heartbeat time.Duration
	// Faults configures message-fault injection (off by default).
	Faults FaultConfig
	// Logger receives structured logs. Defaults to slog.Default().
	Logger *slog.Logger
}

// Broker is an in-memory publish/subscribe relay exposed over HTTP:
//
//	POST /publish        body is one model.Envelope; 202 on accept
//	GET  /subscribe      Server-Sent-Events stream; ?types=a,b filters;
//	                     Last-Event-ID header resumes after that id
//	GET  /healthz        {"status":"ok"} with counters
//
// It is safe for concurrent use.
type Broker struct {
	cfg    BrokerConfig
	log    *slog.Logger
	faults *faults

	mu   sync.RWMutex
	subs map[*subscriber]struct{}
	hist *ring

	published atomic.Int64
	delivered atomic.Int64
	dropped   atomic.Int64 // by fault injection
	duped     atomic.Int64 // by fault injection
	lagged    atomic.Int64 // queued messages discarded from slow subscribers
}

type subscriber struct {
	types map[string]bool // empty => all types
	ch    chan model.Envelope
}

func (s *subscriber) wants(t string) bool {
	return len(s.types) == 0 || s.types[t]
}

// NewBroker returns a Broker ready to serve. It panics only on an invalid
// FaultConfig, which is a programming/config error the caller should have
// caught with FaultConfig.Validate.
func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.Buffer <= 0 {
		cfg.Buffer = defaultBuffer
	}
	if cfg.History <= 0 {
		cfg.History = defaultHistory
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = defaultHeartbeat
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if err := cfg.Faults.Validate(); err != nil {
		panic(err)
	}
	b := &Broker{
		cfg:    cfg,
		log:    cfg.Logger,
		faults: newFaults(cfg.Faults),
		subs:   make(map[*subscriber]struct{}),
		hist:   newRing(cfg.History),
	}
	if cfg.Faults.Enabled() {
		b.log.Warn("event bus fault injection is ENABLED",
			"drop_rate", cfg.Faults.DropRate,
			"duplicate_rate", cfg.Faults.DuplicateRate,
			"delay", cfg.Faults.Delay.String(),
			"reorder", cfg.Faults.Reorder,
			"seed", cfg.Faults.Seed)
	}
	return b
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/publish":
		b.handlePublish(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/subscribe":
		b.handleSubscribe(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		b.handleHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (b *Broker) handleHealth(w http.ResponseWriter, _ *http.Request) {
	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "ok",
		"subscribers": n,
		"published":   b.published.Load(),
		"delivered":   b.delivered.Load(),
		"dropped":     b.dropped.Load(),
		"duplicated":  b.duped.Load(),
		"lagged":      b.lagged.Load(),
	})
}

func (b *Broker) handlePublish(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPublishBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var env model.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "invalid envelope JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := env.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	b.publish(env)
	w.WriteHeader(http.StatusAccepted)
}

// publish records the envelope in history and hands it to every matching
// subscriber's queue. It never blocks: a subscriber that cannot keep up
// loses its oldest queued message (counted as lag), because a slow
// consumer must not stall the bus.
func (b *Broker) publish(env model.Envelope) {
	b.published.Add(1)

	b.mu.Lock()
	b.hist.add(env)
	targets := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		if s.wants(env.Type) {
			targets = append(targets, s)
		}
	}
	b.mu.Unlock()

	for _, s := range targets {
		if !trySend(s.ch, env) {
			// Queue full: discard the oldest, then retry once.
			select {
			case <-s.ch:
				b.lagged.Add(1)
				b.log.Warn("subscriber lagging; dropped oldest queued event",
					"event_id", env.ID)
			default:
			}
			if !trySend(s.ch, env) {
				b.lagged.Add(1)
				b.log.Warn("subscriber queue full; dropped event", "event_id", env.ID)
			}
		}
	}
}

func trySend(ch chan model.Envelope, env model.Envelope) bool {
	select {
	case ch <- env:
		return true
	default:
		return false
	}
}

func (b *Broker) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub := &subscriber{
		types: parseTypes(r.URL.Query().Get("types")),
		ch:    make(chan model.Envelope, b.cfg.Buffer),
	}

	// Register first, then snapshot history, so a message published
	// during setup is delivered live (possibly also replayed — a
	// duplicate, which consumers already tolerate) rather than lost.
	resumeFrom := r.Header.Get("Last-Event-ID")
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	oldest := b.hist.oldestID()
	replay := b.hist.after(resumeFrom)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, sub)
		b.mu.Unlock()
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if resumeFrom != "" && oldest != "" && resumeFrom < oldest {
		b.log.Warn("resume point older than retained history; some events were missed",
			"requested_after", resumeFrom, "oldest_retained", oldest)
	}

	ctx := r.Context()

	// Replay first, honouring the type filter but not fault injection —
	// resumed history should be delivered faithfully.
	for _, env := range replay {
		if !sub.wants(env.Type) {
			continue
		}
		if err := b.writeEnvelope(w, flusher, env); err != nil {
			return
		}
	}

	hb := time.NewTicker(b.cfg.Heartbeat)
	defer hb.Stop()

	// Reorder fault: a held-back event is flushed either when the next
	// event arrives or after reorderMaxHold, whichever comes first, so a
	// trailing held event is never stranded.
	var held *model.Envelope
	var heldC <-chan time.Time
	flush := func(env model.Envelope) error {
		if b.faults.drop() {
			b.dropped.Add(1)
			b.log.Warn("fault: dropped event on delivery", "event_id", env.ID)
			return nil
		}
		if d := b.faults.delay(); d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := b.writeEnvelope(w, flusher, env); err != nil {
			return err
		}
		if b.faults.duplicate() {
			b.duped.Add(1)
			b.log.Warn("fault: duplicated event on delivery", "event_id", env.ID)
			if err := b.writeEnvelope(w, flusher, env); err != nil {
				return err
			}
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			if err := writeSSEComment(w, "ping"); err != nil {
				return
			}
			flusher.Flush()
		case <-heldC:
			if held != nil {
				if err := flush(*held); err != nil {
					return
				}
				held, heldC = nil, nil
			}
		case env := <-sub.ch:
			// Reorder: hold this one back and emit it after the next
			// (or after reorderMaxHold).
			if held == nil && b.faults.holdForReorder() {
				e := env
				held = &e
				heldC = time.After(reorderMaxHold)
				b.log.Warn("fault: holding event back to reorder", "event_id", env.ID)
				continue
			}
			if err := flush(env); err != nil {
				return
			}
			if held != nil {
				if err := flush(*held); err != nil {
					return
				}
				held, heldC = nil, nil
			}
		}
	}
}

func (b *Broker) writeEnvelope(w io.Writer, flusher http.Flusher, env model.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		b.log.Error("marshal envelope for delivery", "event_id", env.ID, "err", err)
		return nil // skip this one, keep the stream alive
	}
	if err := writeSSEEvent(w, env.ID, env.Type, data); err != nil {
		return err
	}
	flusher.Flush()
	b.delivered.Add(1)
	return nil
}

// parseTypes splits a comma-separated ?types= value. Empty => nil (all).
func parseTypes(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make(map[string]bool)
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out[t] = true
		}
	}
	return out
}
