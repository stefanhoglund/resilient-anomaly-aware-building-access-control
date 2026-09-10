// Package model is the shared vocabulary of the access-control system.
//
// It holds the things every service must agree on and nothing else:
//
//   - logical identifiers for rooms, doors and readers (ids.go);
//   - the mapping from those logical ids to BuildSim rooms (mapping.go);
//   - the message envelope carried on the event bus (envelope.go);
//   - the payload of every event type and its spec version (events.go);
//   - the static access-policy configuration types and the minimal-loop
//     data (policy_config.go).
//
// The package has no behaviour beyond pure helpers and no I/O. The
// decision logic that turns policy configuration into an action lives in
// internal/policy, deliberately separate (ADR 0003).
package model
