// Package policy is the deterministic baseline access-control decision.
//
// It is one pure function, Decide, that maps a badge read plus the static
// PolicyConfig to an action (allow / restrict / alert) and the door
// command that action implies. It has no state, no I/O, and no
// randomness, so its whole behaviour is the truth table in ADR 0003 and
// is covered exhaustively by tests.
//
// Anomaly detection is deliberately not here. When the anomaly detector
// arrives (phase 2) it becomes a separate input the decision service
// combines with this baseline; the baseline result is always recorded
// (AccessDecision.BaselineAction) so the two can be compared.
package policy
