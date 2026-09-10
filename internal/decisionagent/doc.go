// Package decisionagent turns observations into access decisions.
//
// It subscribes to badge.read, occupancy.observed and door.state, keeps a
// small in-memory view of the world (each room's latest occupancy, each
// door's latest lock/contact state), and on every badge read runs the
// deterministic baseline policy (internal/policy) to produce an
// access.decision and, when the lock needs to change, a door.command.
//
// Resilience behaviour required by the project:
//
//   - Duplicate badge reads are ignored (dedup on envelope id).
//   - An observation older than the one already applied is rejected
//     (occupancy by event time, door.state by BuildSim's global version).
//   - On startup every door is assumed locked until a door.state says
//     otherwise, so a cold agent never emits a spurious unlock.
//
// Anomaly scoring is deliberately absent. In phase 2 the agent will also
// consume anomaly.scored and combine it with the baseline; the baseline
// result is always recorded (AccessDecision.BaselineAction) so the two
// can be compared.
//
// Handle is driven by a single goroutine (the subscription loop), so the
// type carries no locks.
package decisionagent
