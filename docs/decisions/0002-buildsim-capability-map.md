# 0002 – BuildSim capability map

Status: accepted
Date: 2026-09-10
Supersedes: extends 0001

## Context

Before defining event formats and service interfaces (0003) we need to
know what BuildSim can actually do: what state it holds, what we can
write, how change is observed, and how it validates input. BuildSim ships
no OpenAPI document, so this map was produced by probing the running
instance at `http://127.0.0.1:9090` on 2026-09-10. Everything here is an
observation of that build and must be re-checked if BuildSim is updated;
the `//go:build integration` tests are where that check lives.

## Findings

### Building model (read-only)

| Method | Path | Result |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}`, `Cache-Control: no-store` |
| GET | `/api/building` | `{"name":"A-Building (LTU)","levels":[{"id":"level0","label":"Floor 0"}, level1, level2]}` |
| GET | `/api/building/floors/{level}` | floor plan: `page{width,height}`, `rooms[]` with `id,name,area,center,polygon,type`. level0 has 322 rooms. |
| GET | `/api/building/cross-floor-edges` | stair/connector edges between levels |
| GET | `/api/config` | `{"edit_mode":false,...}` — layout editing is disabled on this instance |

The building layout (levels, rooms, stairs) is fixed. We cannot create
rooms; we can only attach things to rooms that already exist.

Room identifier scheme is **not yet pinned down**: `/api/building/floors/level0`
lists names like `1540`, `1541`, while `/api/building/cross-floor-edges`
uses names like `A1105`, and a `POST /api/equipment` with `room:"A1105"`
on `level0` was accepted while `room:"NOSUCHROOM"` was rejected with
`room "NOSUCHROOM" does not exist on level "level0"`. 0003 must resolve
which identifier form the rest of the system uses. Validation itself is
strict: `level` and `room` must both refer to real building elements.

### Equipment — the only writable surface

`/api/equipment` is the entire control surface BuildSim exposes to us.

| Method | Path | Behaviour |
|---|---|---|
| GET | `/api/equipment` | array of all equipment |
| GET | `/api/equipment/{id}` | one equipment object, or `{"error":"equipment not found"}` 404 |
| POST | `/api/equipment` | create. `201` on success. Requires `id`, `level`, `room`. Duplicate id → `409 equipment already exists`. Unknown room/level → `400`. |
| PUT | `/api/equipment/{id}` | **full-object replace** (not a partial patch — a body without `level`/`room` is rejected `400`). `200` with the updated object. Increments `version`, refreshes every `timestamp`. |
| DELETE | `/api/equipment/{id}` | `200`, or `404` if absent |
| OPTIONS | any `/api/...` | `204` (no usable CORS headers observed — irrelevant to server-side Go clients) |

Equipment object:

```jsonc
{
  "id": "door-A1105",
  "name": "Door A1105",
  "type": "", "category": "", "status": "",   // free-form strings, purpose unclear
  "level": "level0",
  "room": "A1105",
  "version": 9,                                // monotonic, server-owned
  "sensors": [
    { "id": "contact", "name": "Contact",
      "data_type": "binary",                   // only "binary" and "text" are valid
      "binary_value": true,                    // used when data_type == "binary"
      "value": "",                             // string; used when data_type == "text"
      "unit": "",
      "timestamp": "2026-09-10T06:28:31Z" }    // server-set on write
  ],
  "actuators": [
    { "id": "lock", "name": "Lock",
      "state": "unlocked",                     // free-form string
      "timestamp": "2026-09-10T06:28:31Z" }
  ]
}
```

Notes:
- **`data_type` is only `binary` or `text`.** Numeric readings (CO₂,
  temperature, a swipe count) must be carried as `text` `value` plus a
  `unit`, or reduced to a `binary` flag. 0003 decides the encoding.
- **`version` increments on every write** (create counts as 1, each PUT
  +1). This is our freshness / duplicate-suppression primitive — a
  consumer that has already applied version *N* can ignore a re-delivered
  version ≤ *N*, and a writer can detect it has lost a race.
- There is **no sub-resource** for a single sensor or actuator
  (`/api/equipment/{id}/actuators/{aid}` → 404). To change one actuator
  you PUT the whole equipment object back. Concurrent writers to the same
  equipment therefore need coordination — see "Implications".
- PUT does not honour an `If-Match` / `version` precondition in the body
  (a stale `version` field was ignored, the write still applied). Any
  optimistic-concurrency check is ours to build on top.

### Sessions and change notifications

| Method | Path | Behaviour |
|---|---|---|
| POST | `/api/sessions` | `{"id": "<uuid>"}` |
| DELETE | `/api/sessions/{id}` | ends the session |
| WS | `/ws/{id}` | server → client JSON notifications `{type, version, data?}` |

Notification `type` values seen: `equipment`, `doors`, `occupancy`,
`coverage`, `alerts`, `entities`, `effects`, `room_layers`,
`room_appearance`, `viewport`, `highlights`, `route`. The official viewer
treats these purely as "something changed, re-GET it" hints. The socket
is a plausible push alternative to polling `/api/equipment`, but it
requires holding a session and reconnect logic; it is an optimisation,
not a requirement.

### Read-only / not driveable by us

`GET /api/occupancy` (`{}`; shape `{roomKey: {persons: [...]}}`),
`/api/coverage`, `/api/doors`, `/api/alerts`, `/api/entities`,
`/api/room-appearance`, `/api/effects` all return data for the viewer to
render, and **every write method against them returns 404**. On this
build we cannot make BuildSim track occupants, draw alerts, or maintain a
separate "doors" collection for us.

## Implications for our architecture

1. **BuildSim is a room/equipment state store with change hints, nothing
   more.** Doors, badge readers and motion sensors are all modelled as
   `equipment` in a `room`, carrying `sensors[]` and `actuators[]`.

2. **We own occupant simulation entirely.** BuildSim will not move
   occupants or compute occupancy for us, so the occupant/movement
   simulator produces occupant state and emits it as sensor readings on
   equipment (e.g. a motion sensor's `binary_value`). This matches the
   proposal — the simulator is ours.

3. **Serialise writes per equipment.** Because PUT replaces the whole
   object and there is no field-level update or precondition, two
   services writing the same door (one setting a contact sensor, one
   setting the lock actuator) would clobber each other. Options for 0003:
   (a) one "BuildSim gateway" service owns all `/api/equipment` writes
   and everyone else calls it; (b) partition equipment so exactly one
   service writes each piece. (a) also gives us one place to apply
   timeouts, retries and the `version` de-duplication.

4. **`version` is the staleness signal** for the required "stale
   observation" fault tests — consumers compare versions, not wall clock.

5. **Alerts and the dashboard are ours**, served from our own state, not
   from `/api/alerts`.

6. **CORS is not usable**, so the browser dashboard must talk to our own
   backend, never to BuildSim directly — which we wanted anyway.

## Out of scope for this decision

Event schema, the gateway-vs-partition choice, polling vs WebSocket,
retry policy — all deferred to 0003. This document only records what
BuildSim can do.

## Open questions carried to 0003

- Room identifier form (`1540` vs `A1105`) and how we map our logical
  doors to BuildSim rooms.
- Encoding of a badge swipe: `text` sensor holding the card id? a
  `binary` "swipe happened" pulse plus a separate card-id channel?
- Gateway service vs. write partitioning.
- Poll `/api/equipment` on an interval, or hold a session and consume
  `/ws/{id}`.
