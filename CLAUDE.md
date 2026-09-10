# D7065E Project Instructions

This repository implements a distributed cyber-physical building
access-control system for D7065E Embedded Intelligence at the Edge.

## Development principles

- Specification first: do not implement a new component until its
  responsibility and interface have been defined.
- Prefer simple, explicit designs over clever abstractions.
- Every independently deployable component must have one clear responsibility.
- Components communicate only through documented interfaces.
- Do not introduce infrastructure or dependencies without explaining
  the architectural reason.
- Prefer the Go standard library when practical.
- All services must support graceful startup and shutdown.
- All network operations must use timeouts.
- Log important state transitions and decisions using structured logging.
- Never silently discard errors.
- Make failure behavior explicit.
- Keep anomaly detection separate from the autonomous action policy.

## Testing

For every component:
1. unit tests for important logic;
2. interface/contract tests where applicable;
3. at least one integration test;
4. identify relevant failure conditions.

The overall system must be tested for:
- service crashes;
- delayed messages;
- dropped messages;
- duplicate messages;
- stale observations;
- actuator failures.

## AI-assisted development

Do not immediately implement requested features.

For architectural or cross-service changes:
1. inspect the relevant specifications;
2. explain the proposed change;
3. identify affected interfaces;
4. identify tests needed;
5. then implement after the design is coherent.

When generating code, favor code the student can explain during an
individual oral examination.