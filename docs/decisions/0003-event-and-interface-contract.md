# 0003 – Event and service-interface contract (minimal loop)

Status: accepted
Date: 2026-09-10
Builds on: 0001 (BuildSim client), 0002 (BuildSim capability map)

## Purpose

Plan step 2: "define event formats and service interfaces." This
document fixes

1. the message envelope and the event types for the minimal loop,
2. the responsibility and interface of each service,
3. the deterministic decision policy for the first implementation,
4. the transport for phase 1 and why,
5. the tests and failure conditions that follow.

It deliberately specifies the **target** contract but scopes the **first
implementation** to the smallest set that closes the loop (0004 will be
the first implementation itself).

## Resolved open questions from 0002

### Room identity

`/api/building/floors/level0` shows room names in two schemes (`1540…`
and `A1000A…`) because several wings share one floor plan, and some names
repeat (`1552` appears four times). Therefore:

- The unique key for a BuildSim room is `(level, room-id)`, not the name.
  `POST /api/equipment` takes the room **name**, so the minimal loop only
  uses rooms whose name is unique on their level.
- Our system carries its own stable logical identifiers
  (`door-1`, `reader-1`, `room-office`, `room-lab`) and keeps a small
  static mapping table to BuildSim `(level, room-name)`. Services never
  hard-code BuildSim names.

**Chosen (verified against the running instance):** `level0` rooms
`A1006` (id 61) and `A1007` (id 62) — adjacent in the walkable graph,
both names unique on the level, both accept equipment. Mapping:

| logical id | BuildSim | role |
|---|---|---|
| `room-office` | `level0` / `A1006` | permitted office |
| `room-lab` | `level0` / `A1007` | restricted lab |
| `door-1` | equipment in `A1007` | the controlled door between them |
| `reader-1` | sensors on `door-1` | badge reader at `door-1` |

### Badge-swipe encoding

A badge read is modelled on the door equipment as **two sensors**:

- `badge` – `data_type: "text"`, `value` = card id of the last swipe
  (empty string = no swipe yet);
- `badge_pulse` – `data_type: "binary"`, flipped `true` on a swipe and
  back to `false` after it is processed, so a repeated swipe of the same
  card is still observable as a new event.

The authoritative badge event travels on the bus (below); the equipment
sensors exist so the BuildSim viewer reflects reality.

### Who writes BuildSim

For the minimal loop **only the door-actuator writes BuildSim**, and it
writes the whole door-equipment object (contact sensor, `badge`,
`badge_pulse`, `lock` actuator) each time. This sidesteps the
whole-object-replace clobber problem from 0002 without introducing a
gateway service yet.

The "BuildSim gateway service vs. write partitioning" decision is
**deferred to phase 3**, when the occupant simulator and occupancy
sensors also need to write. Until then the writer lives in
`internal/buildsim` as added functions, not a process.

### Transport for phase 1

**A single in-repo event-bus service, stdlib `net/http` + JSON, no
broker.**

- `POST /publish` – body is one envelope; returns `202`.
- `GET /subscribe?types=badge.read,occupancy.observed` – Server-Sent
  Events stream of matching envelopes.
- `GET /healthz`.

Rationale (CLAUDE.md: no infra without an architectural reason):

- Phase 1 has one producer and two consumers; a broker (MQTT) is not yet
  justified.
- The required fault tests — delayed, dropped, duplicated, out-of-order
  messages — need **one controllable chokepoint**. Owning the bus gives
  us a `FAULT_*` config to inject exactly those, deterministically, in
  integration tests. A third-party broker makes this harder, not easier.
- The **envelope is transport-independent**. Moving to MQTT later (when
  there are many-to-many flows and we want QoS/retained messages) means
  swapping the publish/subscribe adapter, not touching event definitions
  or service logic. That swap gets its own ADR when the need is real.

Delivery guarantee in phase 1: **at-least-once, unordered**. Every
consumer must be idempotent on `envelope.id` and tolerate reordering by
using `occurred_at` and version fields, never arrival order.

## The envelope

Every message on the bus is:

```jsonc
{
  "id": "01J...",            // ULID, unique per event; the dedup key
  "type": "badge.read",      // dotted, lower-case; table below
  "source": "simulator",     // logical service name that produced it
  "occurred_at": "2026-09-10T12:00:00.000Z", // RFC3339 UTC, event time (not send time)
  "spec_version": 1,         // bump on any breaking change to a payload
  "data": { /* type-specific, see below */ }
}
```

- `id` is a ULID so it is sortable by creation time and cheap to
  generate without coordination.
- `occurred_at` is **event time**. A delayed message keeps its original
  `occurred_at`; that is how a consumer recognises it as stale.
- Unknown `type` or higher `spec_version` than a consumer understands →
  the consumer logs and skips it. It never crashes and never silently
  drops without logging.

## Event types (minimal loop)

| `type` | producer | consumers | `data` |
|---|---|---|---|
| `badge.read` | simulator | decision-agent | `{ reader_id, door_id, room_id, card_id, occupant_id }` |
| `occupancy.observed` | simulator | decision-agent | `{ sensor_id, room_id, present: bool, occupant_count: int }` |
| `access.decision` | decision-agent | door-actuator, (dashboard later) | `{ decision_id, card_id, door_id, room_id, action: "allow"\|"restrict"\|"alert", reason: string, evidence: {…}, baseline_action: string }` |
| `door.command` | decision-agent | door-actuator | `{ command_id, decision_id, door_id, desired_lock: "locked"\|"unlocked", ttl_seconds: int }` |
| `door.state` | door-actuator | decision-agent, (dashboard later) | `{ door_id, lock: "locked"\|"unlocked", contact: "open"\|"closed", source: "actuator", buildsim_version: int, applied: bool, error: string }` |

Notes:

- `access.decision` carries both the chosen `action` and, separately,
  `baseline_action` — so evaluation can compare the deterministic
  baseline with whatever the anomaly-aware policy later decides. This is
  where the "keep detection separate from policy" rule shows up in the
  schema: the decision record always states its evidence and what the
  plain baseline would have done.
- `door.command.ttl_seconds` lets a "restrict" be temporary (proposal:
  "temporarily restrict the door").
- `door.state.applied=false` with `error` set is how an **actuator
  failure** is reported — explicit, on the bus, never a dropped error.
- `anomaly.scored` is **intentionally absent** from the minimal loop. The
  anomaly detector is added in phase 2 as a new producer of
  `anomaly.scored`, consumed by the decision-agent as *additional
  evidence*. The loop must work with the deterministic baseline alone
  first.

## Services

Each is one `cmd/<name>` process, one responsibility, config from
environment, structured `slog` logging, graceful shutdown, timeouts on
every network call (per CLAUDE.md).

### `cmd/simulator` — occupant & stimulus simulator

- Owns occupant state. Phase-1 scenario: **one occupant, two rooms, one
  door.** Drives a scripted day: occupant badges into the office
  (permitted), moves, then badges at the restricted lab at an unusual
  hour (the injected anomaly).
- Produces `badge.read` and `occupancy.observed`.
- Scenario is data-driven (a JSON/YAML scenario file) so the same run is
  repeatable; a seed controls any jitter.
- Interface: publishes to the bus; exposes `/healthz`. Consumes nothing.

### `cmd/decision-agent` — world state + deterministic policy

- Subscribes to `badge.read`, `occupancy.observed`, `door.state`.
- Maintains in-memory world state keyed by `door_id` / `room_id`, each
  entry tagged with the `occurred_at` and version of the last applied
  observation. **Rejects an observation older than the one already
  applied** (stale-observation handling).
- On a `badge.read`, evaluates the baseline policy (below), produces one
  `access.decision` and, when the action changes the lock, one
  `door.command`.
- Idempotent on `envelope.id`: a duplicate `badge.read` yields no second
  decision.
- Keeps anomaly scoring **out**: in phase 2 it will also subscribe to
  `anomaly.scored` and combine it, but the baseline path stays intact and
  is always recorded.

### `cmd/door-actuator` — the only BuildSim writer in phase 1

- Subscribes to `door.command`.
- Ensures the door equipment exists (`POST /api/equipment` once, `409`
  tolerated), then `PUT`s the full object to set the `lock` actuator and
  reflect the last known sensors.
- Idempotent on `command_id` **and** on desired state: commanding
  `locked` when already `locked` is a no-op that still emits `door.state`.
- Every BuildSim call has a timeout (reuses `internal/buildsim` +
  `BUILDSIM_TIMEOUT`). On failure: a bounded number of retries with
  backoff, then emit `door.state{applied:false,error:…}` and keep serving.
- Publishes `door.state` after every attempt.

### `internal/buildsim` additions (library, not a service)

- `EnsureEquipment(ctx, Equipment) error` — create if absent, ignore
  `409`.
- `PutEquipment(ctx, Equipment) (Equipment, error)` — full replace,
  returns the object with its new `version`.
- Types for `Equipment`, `Sensor`, `Actuator` matching 0002.

### `internal/model` — shared vocabulary

- Logical ids and the static `door_id/room_id → (level, buildsim room
  name)` mapping.
- Envelope + event payload structs, one `spec_version` constant.
- No behaviour, no I/O — just the types both sides of every interface
  agree on. Contract tests live next to it.

### `internal/eventbus` + `cmd/eventbus`

- `internal/eventbus`: `Client` with `Publish(ctx, Envelope) error` and
  `Subscribe(ctx, types []string) (<-chan Envelope, error)` (SSE under
  the hood, reconnect with resume).
- `cmd/eventbus`: the relay process. Config `EVENTBUS_ADDR`, and
  `FAULT_DUPLICATE`, `FAULT_DROP_RATE`, `FAULT_DELAY_MS`,
  `FAULT_REORDER` for tests — **off by default**, logged loudly when on.

## Deterministic baseline policy (first implementation)

Inputs: `card_id`, `door_id`/`room_id`, `occurred_at`, current world
state (recent occupancy for the room, last door state).

Static config (a JSON file in `internal/model` for phase 1):

- `permissions`: `card_id → set of room_id` it may enter;
- `room_windows`: `room_id → allowed local time window` (e.g. lab
  `07:00–19:00`), default "any";
- `restricted_rooms`: set of `room_id` that always alert on a
  non-permitted attempt.

Decision:

| Condition | `action` | `door.command` |
|---|---|---|
| card permitted for room **and** within window | `allow` | `unlocked` (ttl short) |
| card **not** permitted for room, room not restricted | `restrict` | `locked` |
| card permitted but **outside** window | `restrict` | `locked` |
| card not permitted **and** room restricted | `alert` | `locked` |
| badge read but occupancy contradicts it (claims entry, room shows no one for N seconds after) — deferred check | `alert` | keep current |

`reason` is a short human string; `evidence` is the structured inputs
that led to the choice. `baseline_action` == `action` in phase 1 (they
diverge once the anomaly detector is in).

The phase-1 anomaly the simulator injects — restricted lab, unusual hour,
non-permitted card — lands in the last row: `alert` + stay `locked`.

## Affected files

New:

- `docs/decisions/0003-event-and-interface-contract.md` (this file)
- `internal/model/{ids.go,mapping.go,envelope.go,events.go,policy_config.go}` + tests
- `internal/eventbus/{client.go,sse.go}` + tests
- `internal/buildsim/equipment.go` (+ `equipment_test.go`) — new funcs & types
- `internal/policy/{baseline.go}` + tests (pure decision function)
- `cmd/eventbus/main.go` (+ fault-injection, tests)
- `cmd/simulator/main.go` (+ scenario loader, tests)
- `cmd/decision-agent/main.go` (+ world-state + wiring tests)
- `cmd/door-actuator/main.go` (+ tests)
- `test/integration/minimal_loop_test.go` (`//go:build integration`)
- `test/fault-injection/*` (`//go:build fault`)
- `deployments/` — compose file for the five processes + BuildSim URL

Changed:

- `internal/buildsim/` gains equipment types/functions (client stays
  as-is).
- `.env.example` gains `EVENTBUS_ADDR`, scenario path, `FAULT_*`.

Unchanged interfaces: the `buildsim.Client` HTTP contract from 0001.

## Tests this contract requires

Per component (CLAUDE.md):

- `internal/model`: JSON round-trip / contract tests for the envelope and
  every payload; unknown-field and version-skew behaviour.
- `internal/policy`: truth-table unit tests for every row above, plus
  boundary times and the stale-input rule.
- `internal/eventbus`: publish→subscribe delivery, type filtering,
  subscriber reconnect/resume, slow-subscriber backpressure.
- `internal/buildsim`: `EnsureEquipment` idempotency (`409`), `PutEquipment`
  version bump, timeout, non-2xx (extends 0001 tests).
- `cmd/decision-agent`: duplicate `badge.read` → one decision; stale
  `occupancy.observed` ignored; `door.state` updates world state.
- `cmd/door-actuator`: duplicate `command_id` → one write; BuildSim 5xx →
  retry then `door.state{applied:false}`; already-in-state command →
  no-op + `door.state`.
- `cmd/simulator`: scenario file drives the exact expected event
  sequence for a fixed seed.

System-level (CLAUDE.md list):

| Requirement | How this contract lets us test it |
|---|---|
| service crashes | kill `decision-agent` mid-scenario, restart, assert loop recovers (world state rebuilt from re-subscribed stream) |
| delayed messages | `FAULT_DELAY_MS` on the bus; assert stale observation rejected by `occurred_at` |
| dropped messages | `FAULT_DROP_RATE`; assert a dropped `door.command` leaves the door in a safe state and is detectable via missing `door.state` |
| duplicate messages | `FAULT_DUPLICATE`; assert single decision / single BuildSim write |
| stale observations | replay an old `occupancy.observed`; assert world state unchanged |
| actuator failures | point actuator at a BuildSim URL that 500s; assert `door.state{applied:false,error}` and no crash |

## Failure conditions identified

- Bus process down at publish → producer buffers up to a bound, then logs
  and drops with a counter (never blocks the simulator forever).
- Bus up, subscriber slow → bus drops oldest for that subscriber and
  flags `lag` in `door.state`/logs.
- `decision-agent` restart → cold world state; must not emit spurious
  `unlock`. Rule: on start it assumes every door `locked` until it
  observes otherwise.
- `door.command` for an unknown `door_id` → `door.state{applied:false,
  error:"unknown door"}`.
- BuildSim room name ambiguity (duplicate names) → mapping table
  validated at actuator startup against `/api/building/floors/{level}`;
  refuse to start on an ambiguous mapping.
- Clock skew between processes → all comparisons use `occurred_at` from
  one source per entity where possible; document that simulator time is
  the reference in phase 1.

## Out of scope (later ADRs)

- Anomaly detector and `anomaly.scored` (phase 2).
- Storage / feature pipeline (phase 3).
- BuildSim gateway vs. write partitioning (phase 3).
- MQTT migration (only if a real many-to-many need appears).
- Dashboard (phase 3).
