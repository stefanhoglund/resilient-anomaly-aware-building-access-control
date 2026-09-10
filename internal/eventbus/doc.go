// Package eventbus is the phase-1 transport for the access-control
// system (ADR 0003).
//
// It provides both halves of a minimal publish/subscribe bus:
//
//   - Broker is an http.Handler that accepts envelopes on POST /publish
//     and fans them out to Server-Sent-Events streams on GET /subscribe.
//     cmd/eventbus wraps it in a process.
//   - Client publishes envelopes and opens subscriptions, reconnecting
//     and resuming automatically.
//
// Delivery is at-least-once and unordered: every consumer must be
// idempotent on the envelope id and must order events by their
// occurred_at / version fields, never by arrival. The broker keeps a
// bounded history so a subscriber that reconnects with a Last-Event-ID
// resumes without losing messages inside the retention window.
//
// The broker can inject the faults the project must be tested against —
// dropped, delayed, duplicated and reordered messages — under explicit
// configuration. Fault injection is off unless configured and is logged
// loudly when on. See faults.go.
//
// No third-party dependencies: SSE framing is a few dozen lines and is
// implemented here (CLAUDE.md: prefer the standard library).
package eventbus
