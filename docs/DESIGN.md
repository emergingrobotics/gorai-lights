# gorai-lights — Design

Implements [../REQUIREMENTS.md](../REQUIREMENTS.md) (v3). North star: [../../gorai/VISION.md](../../gorai/VISION.md).

## 1. Module map

```
gorai-lights (this module, single binary)
  main.go                         blank-imports the pieces, calls gorai.Run()
  internal/schedule/              PURE logic — no NATS, no I/O (unit-tested in isolation)
    timespec.go                   parse "HH:MM" | "sunrise|sunset[±HH:MM]"
    window.go                     {on,off} window, midnight-to-midnight, no wrap
    plan.go                       resolve timespecs→absolute times for a day; desired state
  internal/solar/                 lat/long+date → sunrise/sunset (wraps go-sunrise)
  internal/tz/                    lat/long → IANA zone (wraps bradfitz/latlong), + override/UTC fallback
  services/lights/                gorai service "scheduler/lights": reconcile loop, PUBLISHES commands
    lights.go                     service lifecycle, GPS subscription, reconcile/evaluate loop
    config.go                     parse + validate RDL attributes (lights[], windows, location)
  services/tasmotasim/            gorai service "tasmota/sim": SUBSCRIBES to <device>.command,
                                  simulates the bulb (the hardware-free command consumer)
  test/                           test harness (REQUIREMENTS §11) — not in the production build
    gpsinject/                    component "gps/inject": publishes synthetic NMEA for any lat/long/time
    natslisten/                   recorder: subscribes to tasmota command subjects, wait-for primitive
    harness/                      embedded NATS + real scheduler wiring; BootFixture via the runtime
    timezones/                    the every-timezone matrix test
    robots/                       robot.json fixtures (embedded) + the runtime smoke test

gorai-tasmota (sibling module)
  internal/device/                Device iface, Sonoff/Kauf, NewDevice, Action/Color (currently internal)
  service/controller/             gorai service "tasmota/controller": SUBSCRIBES to <device>.command,
                                  drives real devices over HTTP; registers via init() (built + tested)
```

`gorai-lights/main.go` blank-imports: gorai runtime, `gorai-gps/component/nmea`,
`gorai-lights/services/lights`, and `gorai-lights/services/tasmotasim`.

**Driver selection.** The binary registers **both** Tasmota drivers and the robot config picks one
per service: the real HTTP driver `gorai-tasmota/service/controller` (`tasmota`/`controller`, used
by `robot.json`) and the in-process `tasmota/sim` (`tasmota`/`sim`, used by `robot.test.json`). Both
subscribe to the same `…tasmota.<device>.command` subject — the only difference is HTTP-to-bulb vs
simulated I/O — so the NATS-command architecture (REQ-ARCH-2/3/4, REQ-MOD-1) is identical either
way. `make build` produces the controller-backed binary; `make run-test` runs the sim config.
REQ-MOD-2 (moving `internal/device` to a public package) is not required — gorai-lights imports the
controller *service*, not the device types.

## 2. Control contract (the seam)

NCP tool on the embedded bus — the only coupling between scheduler and driver, and the
public surface other components use (REQ-ARCH-3/4):

- Subject: `gorai.<namespace>.tasmota.<device>.command`
- Payload: `{"state":"on"|"off"[,"brightness":0-255,"color":{"r","g","b"}]}`
- Publisher: scheduler (and anyone). Subscriber: tasmota controller. Fire-and-forget, idempotent.

`<namespace>` is the robot's effective namespace (`subjects.NewBuilder`), identical on both sides.

## 3. Data flow

```
gps component ──Position()──► scheduler ──(daily)──► solar.Times(lat,lng,date)
                              scheduler ──(once/fix)─► tz.Lookup(lat,lng) ► time.Location
reconcile tick (default 60s):
  for each light: plan.DesiredState(now, windows, tz, solar) → on/off
     if changed since last (or re-assert): nc.Publish(<device>.command, {state})
                                                   │
tasmota controller (subscribed) ◄─────────────────┘
  device.NewDevice(type,addr).Execute({state,...}) → HTTP to bulb
```

## 4. gorai-tasmota refactor (REQ-MOD)

- Move `internal/device` → `device/` (public). It already only depends on a config
  `Action`/`Color`; move those into the `device` package (or a public `tasmota` pkg) so the
  public API is self-contained and NATS-free.
- Add `device.Device.Status(ctx) (on bool, err error)` (Sonoff `GET /cm?cmnd=Power` → parse
  `{"POWER":"ON"}`; Kauf ESPHome state GET) — optional for the scheduler, used by the
  controller to skip redundant writes.
- Add `service/controller` registering `("tasmota","controller")`. Constructor reads
  `attributes.devices[]`, builds a `device.Device` per entry, and for each subscribes to
  `gorai.<ns>.tasmota.<name>.command`; on message: validate/clamp → `Execute`. Gets `nats`/
  `logger` from `deps` (same pattern as the existing service).
- Keep the existing `service` (suntimes `automation/tasmota`) untouched and off the
  gorai-lights import path.

## 5. Scheduler service (services/lights)

- Constructor (`registry.RegisterService("scheduler","lights",New)`): parse attributes into a
  typed `Config` (timezone?, gps name, reconcile_interval, location?, lights[]), validate
  (REQ-CFG-9, REQ-CFG-6 no-wrap), grab `nats`/`logger` from deps, build a `subjects.Builder`
  from the namespace.
- **Position source.** The GPS component publishes NMEA on `gorai.<ns>.<gps>.data`; the scheduler
  subscribes and parses latitude/longitude with `gorai-gps/pkg/gps` (`ParseRMC`/`ParseGGA`). GPS
  time is ignored — v3 uses the system clock (REQ-TIME-2). A configured `location` is the fallback
  position when there is no live fix.
- `Start(ctx)` = `SubscribeGPS()` + launch the reconcile goroutine. `SubscribeGPS()` is exported
  so the test harness can receive injected fixes without the system-clock loop; `EvaluateOnce(now,
  reassert)` runs a single evaluation at a chosen instant for deterministic testing; `Position()`
  exposes the current fix. These are the only test seams (REQUIREMENTS §11).
- Reconcile: evaluate every second to catch crossed triggers (publish on change + on startup),
  re-assert all lights every `reconcile_interval`. tz resolves once (override → GPS lookup → UTC,
  cached/final once non-degraded); solar recomputes at the local-day rollover and on first fix.
- **Deferred = unknown, not off.** When a light's only active determinant is a solar window with no
  position yet, desired state is *unknown*: the scheduler publishes neither on nor off for that
  light (REQ-SOLAR-4 "deferred, not driven"). An affirmative on (from any resolved window) or a
  fully-known off is published; clock windows always resolve.
- Pure decisioning lives in `internal/schedule` so it's unit-tested without NATS/GPS.

### 5.1 Known limitation (REQ-CFG-4)
The scheduler and the device driver are decoupled by NATS and may live in different binaries, so
the scheduler cannot validate at startup that a light's `device` exists in some controller's
`devices[]`. A mismatched name simply publishes to a subject no driver consumes (logged). Strict
cross-service validation would require mesh discovery and is deferred.

## 6. State model

- Per-light: `lastPublished` state (in-memory) to detect change; cleared never persisted.
- `tz *time.Location` (resolved once / on fix). `solar` cache: `{date, sunrise, sunset}` for
  the location, recomputed at local-midnight rollover and on first fix.
- No database. No persisted overrides (deferred).

## 7. Dependency on gorai-tasmota

- `main.go` blank-imports `gorai-tasmota/service/controller`, so the binary depends on the sibling
  module. gorai-tasmota is a **public, tagged** repo, so it is pulled like any normal dependency via
  the Go module proxy — no `replace`, no `go.work`:
  ```
  require github.com/emergingrobotics/gorai-tasmota v0.1.0
  ```
- **Environment note:** a global git rewrite `url.ssh://git@github.com/.insteadof
  https://github.com/` forces Go's *direct* VCS mode to SSH, which fails without a key. Resolving
  through the module proxy (the default `GOPROXY=https://proxy.golang.org,direct`) avoids direct
  mode entirely, so `make build`/`make tidy` work with no GitHub credentials. Only pin by branch
  name (`@master`) if you have SSH/token auth, since that can trigger direct VCS resolution.

## 8. External dependencies (new)

- `github.com/nathan-osman/go-sunrise` — pure-Go sunrise/sunset (lat,lng,date → UTC). MIT.
- `github.com/bradfitz/latlong` — embedded lat/long → IANA zone (~80 KB), offline.

## 9. Error handling & safety (maps to Go rules)

- All device HTTP via the device package's `*http.Client` with a 10s timeout (CWE-400).
- No `InsecureSkipVerify`; LAN HTTP only (devices are plain HTTP — documented; no secrets sent).
- Controller validates/clamps command payloads at the boundary (state∈{on,off}, brightness 0–255).
- Per-light failures isolated and logged; never crash the robot. No secrets in config or logs.
- No DB, no shell, no user file paths → SQLi/command-injection/path-traversal not applicable.
