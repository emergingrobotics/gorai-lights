# gorai-lights — Test Plan

Covers [../REQUIREMENTS.md](../REQUIREMENTS.md) (v3). Tests are written **before** implementation
(TDD). Run via `make test`. Target: every pure decision path and every command/validation path
has a unit test; one integration test exercises schedule→NATS→controller end-to-end.

## Phase A — gorai-tasmota module (device + controller)

**A1. device package (table tests, `httptest.Server`)**
- Sonoff `Execute({on})` → GET `/cm?cmnd=POWER+ON`; `{off}` → `POWER+OFF`; 2xx=success, 5xx=error.
- Kauf `Execute({on,brightness,color})` → POST `turn_on?brightness&r&g&b`; `{off}` → `turn_off`.
- `NewDevice` unknown type → error.
- `Status()` parses Sonoff `{"POWER":"ON"}`→true / `"OFF"`→false; Kauf state endpoint.
- Client honors timeout (slow server → error, bounded).
- Validation/clamp: brightness <0 or >255 rejected/clamped; unknown state rejected.

**A2. controller service**
- Construct from `attributes.devices[]`; missing nats dep → error; missing `address` → error.
- On a command message to `…tasmota.<name>.command`, the device is driven (fake device / dry-run
  records the call); malformed JSON → ignored + logged; unknown state → no device call.
- Subscribes to one subject per configured device.

## Phase B — gorai-lights pure schedule logic (`internal/schedule`, no I/O)

**B1. timespec parsing**
- `"18:30"` → clock(18,30); `"24:00"` valid as off-boundary; `"7:5"`/`"25:00"`/`"18:60"` → error.
- `"sunset"`/`"sunrise"` → event, zero offset.
- `"sunset-00:30"` → event=sunset, offset=-30m; `"sunrise+00:10"` → +10m; bad offset → error.

**B2. window validation (REQ-CFG-6 no wrap)**
- resolved `on < off` ok; `on==off`/`on>off` → error naming the window.
- clock-only window resolves without solar; solar window needs solar times.

**B3. desired-state (`plan.DesiredState`)**
- now inside a single window → ON; outside → OFF.
- multiple windows per day; overlapping windows union → ON.
- boundaries: at `on` → ON; at `off` → OFF (`[on,off)`).
- solar window: with injected sunrise/sunset, on at sunset-15m etc.
- offset applied correctly; DST day (tz with transition) resolves clock times correctly.
- no-wrap enforced: two windows covering 22:00–24:00 and 00:00–06:00 behave per-day.

## Phase C — solar + tz wrappers

**C1. solar** — `Times(lat,lng,date)` returns sunrise<sunset for a mid-latitude date (compare to
known value within tolerance); polar latitude → no-event sentinel handled.
**C2. tz** — `Lookup(lat,lng)` returns expected IANA zone for a few known coordinates (SF→
`America/Los_Angeles`); ocean/no-match → fallback (override or UTC); explicit override wins.

## Phase D — scheduler service (with embedded NATS)

**D1.** Construct from RDL attributes; invalid config (off≤on, unknown device ref, empty schedule,
bad timespec) → startup error naming the light.
**D2.** With an in-process NATS server: a window whose `on` time is "now" causes a publish of
`{"state":"on"}` to `gorai.<ns>.tasmota.<device>.command`; state change → publish; no change → no
publish (except re-assert tick). Startup publishes current desired state.
**D3.** No GPS fix + no `location` → solar windows deferred (no publish, logged); clock windows
still publish. Fallback timezone used until GPS resolves.

## Phase E — integration (one binary path, embedded NATS)

**E1.** Wire scheduler + tasmota controller (dry-run device) on one embedded NATS; drive a clock
window edge; assert the controller received the command and (dry-run) "drove" the device.
**E2.** `gorai run robot.test.json` via the built binary boots with the GPS simulator and both
services with no errors (smoke test; documented manual/CI step).

## Coverage & gates
- `make test` MUST pass with zero failures before each phase boundary and before review.
- Pure packages (`internal/schedule`, `solar`, `tz`, `device`) target high line coverage;
  service packages covered by D/E.
- A failing test triggers `systematic-debugging` (root cause) before any fix.

## Implementation status (as built)

- **Phase A (device + controller)** — covered in the **gorai-tasmota** module: `internal/device`
  table tests + `service/controller` unit tests, plus the new self-contained integration
  framework `gorai-tasmota/test/integration` (embedded NATS drives the real controller against 10
  HTTP-mocked devices; `make test-integration`). See `gorai-tasmota/REQUIREMENTS.md` §1.
- **Phase B (pure schedule logic)** — `internal/schedule/schedule_test.go`: timespec parsing,
  static no-wrap validation, desired-state membership/boundaries, multiple/overlapping windows,
  solar offsets, DST day, deferral, solar no-wrap violation.
- **Phase C (solar + tz)** — `internal/solar/solar_test.go` (mid-latitude + polar) and
  `internal/tz/tz_test.go` (known coordinates, ocean no-match, override→lookup→UTC precedence).
- **Phase D (scheduler service, embedded NATS)** — `services/lights/lights_test.go`: invalid-config
  rejection, clock-window on/off/no-change/re-assert, solar deferral without position, location
  fallback, GPS RMC parsing (valid + void).
- **Phase E (integration) + REQUIREMENTS §11 harness** — under `test/`:
  - `gpsinject` — synthetic NMEA injector (round-trip verified against `gorai-gps`).
  - `natslisten` — wildcard command/state recorder with a deterministic wait-for primitive.
  - `harness` — embedded NATS + the real scheduler wiring, and `BootFixture` which boots a
    `robot.json` through the **real gorai runtime**.
  - `timezones` — the every-timezone matrix (17 zones incl. half-hour offset and the dateline),
    DST both-sides, and ocean / override / polar fallbacks (REQ-TEST-11..13).
  - `robots` — embedded `robot.json` fixtures (clock, solar, multi-window, tz-override, no-fix,
    smoke) + a runtime smoke test that boots `smoke.json` and asserts the simulated light is driven.

All of the above pass under `make test` with no hardware, no external NATS, and no network egress.
