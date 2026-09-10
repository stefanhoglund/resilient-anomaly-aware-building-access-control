package eventbus

import "github.com/stefanhoglund/resilient-anomaly-aware-building-access-control/internal/model"

// ring is a bounded FIFO history of recently published envelopes. It lets
// a subscriber that reconnects with a Last-Event-ID resume without losing
// messages, as long as they are still inside the retention window.
//
// It is not safe for concurrent use; the broker holds its lock around
// every call.
type ring struct {
	buf  []model.Envelope
	size int
}

func newRing(size int) *ring {
	if size < 1 {
		size = 1
	}
	return &ring{buf: make([]model.Envelope, 0, size), size: size}
}

// add appends env, evicting the oldest entry when full.
func (r *ring) add(env model.Envelope) {
	if len(r.buf) == r.size {
		copy(r.buf, r.buf[1:])
		r.buf = r.buf[:r.size-1]
	}
	r.buf = append(r.buf, env)
}

// after returns, in order, every retained envelope whose id sorts after
// afterID. Envelope ids are ULIDs, so a lexical comparison is a
// chronological one. An empty afterID returns nothing (a fresh
// subscription with no resume point gets live traffic only).
func (r *ring) after(afterID string) []model.Envelope {
	if afterID == "" {
		return nil
	}
	var out []model.Envelope
	for _, e := range r.buf {
		if e.ID > afterID {
			out = append(out, e)
		}
	}
	return out
}

// oldestID returns the id of the oldest retained envelope, or "".
func (r *ring) oldestID() string {
	if len(r.buf) == 0 {
		return ""
	}
	return r.buf[0].ID
}
