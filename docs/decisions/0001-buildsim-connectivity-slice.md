# 0001 – BuildSim connectivity slice

Status: accepted
Date: 2026-09-10

## Context

The system must talk to BuildSim, an external HTTP simulator of the
physical building (running locally at `http://127.0.0.1:9090`). Before
building any real service we need a minimal vertical slice that proves a
Go process can be configured for, reach, and correctly parse BuildSim.

BuildSim exposes no OpenAPI document. The schema below is inferred from
observing the running instance and must be guarded by a contract test.

Observed endpoints used by this slice:

| Method | Path            | 200 body                                                                 |
|--------|-----------------|-------------------------------------------------------------------------|
| GET    | `/healthz`      | `{"status":"ok"}` (`Cache-Control: no-store`)                          |
| GET    | `/api/building` | `{"name":"A-Building (LTU)","levels":[{"id":"level0","label":"Floor 0"}]}` |

## Decision

Add one package and one binary.

### `internal/buildsim` — the only component that knows BuildSim's HTTP shape

Responsibility: turn BuildSim's HTTP API into typed Go values and typed
errors. It owns the base URL, the HTTP client, timeouts, status-code
checking and JSON decoding. Nothing else in the system imports
`net/http` to reach BuildSim.

Interface (concrete type `*buildsim.Client`):

- `New(Config) (*Client, error)`
- `Health(ctx context.Context) (Health, error)`
- `Building(ctx context.Context) (Building, error)`

Consumers declare their own narrow interface over these methods so they
can substitute a fake in tests. The package does not export a catch-all
interface.

Configuration (`ConfigFromEnv`):

- `BUILDSIM_URL` — **required**. Parsed and scheme-checked; missing or
  malformed is a fatal startup error.
- `BUILDSIM_TIMEOUT` — optional Go duration, default `5s`. Applied as
  both the `http.Client` timeout and, per call, via `context`.

Failure behavior is explicit: any non-2xx response, transport error,
timeout, or JSON decode error is returned as a wrapped error naming the
endpoint. No error is silently discarded. `Health` returns the parsed
value even when `status != "ok"`; deciding what to do about an unhealthy
BuildSim is the caller's job (`Health.OK()` helper provided).

### `cmd/buildsim-probe` — the vertical slice binary

A short-lived diagnostic process: load config from the environment,
construct a `Client`, call `Health` then `Building`, log each outcome as
a structured event (`log/slog`, JSON, stdout), exit `0` on success and
`1` on any failure. It installs a SIGINT/SIGTERM-cancelled context so an
in-flight request is abandoned cleanly on shutdown. Real logic lives in
`run(ctx, logger) int`; `main` only wires and calls `os.Exit`.

## Out of scope for this slice

- `/api/doors` and any other BuildSim endpoint.
- Authentication (BuildSim currently needs none).
- Any write / actuation call.
- Message bus, event modelling, anomaly detection, action policy.
- Retries and circuit breaking — a single attempt is enough to prove
  connectivity; resilience is a later, separately specified concern.

## Consequences

- Stdlib only; `go.mod` gains no dependencies.
- Config is 12-factor (env only, no hardcoded host), logs go to stdout,
  the binary is pure Go — so it containerizes into a `scratch` image
  later without code change.
- If BuildSim's schema changes, the `//go:build integration` contract
  test in `test/integration` fails against the live instance while the
  hermetic unit tests keep passing — that divergence is the signal.
