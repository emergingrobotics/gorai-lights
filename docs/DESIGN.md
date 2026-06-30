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
  service/lights/                 gorai service "scheduler/lights": reconcile loop, PUBLISHES commands
                                  registers via init()

gorai-tasmota (sibling module — refactored, see §4)
  device/                         PUBLIC: Device iface, Sonoff/Kauf, NewDevice, Action/Color, Status
  service/controller/             gorai service "tasmota/controller": SUBSCRIBES to <device>.command,
                                  drives devices; registers via init()
```

`gorai-lights/main.go` blank-imports: gorai runtime, `gorai-gps/component/nmea`,
`gorai-tasmota/service/controller`, and `gorai-lights/service/lights`.

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

## 5. Scheduler service (service/lights)

- Constructor (`registry.RegisterService("scheduler","lights",New)`): parse attributes into a
  typed `Config` (timezone?, gps name, reconcile_interval, location?, lights[]), validate
  (REQ-CFG-9, REQ-CFG-6 no-wrap), grab `nats`/`logger` from deps, build a `subjects.Builder`
  from the namespace. Resolve the GPS component (`deps.Get(gpsName)` → `sensor.GPS`).
- `Start(ctx)`: launch the reconcile goroutine.
- Reconcile tick: ensure tz resolved (override → GPS lookup → UTC); ensure today's solar
  computed (needs a fix or `location` fallback); for each light compute desired state and
  publish on change (and re-assert each tick).
- Pure decisioning lives in `internal/schedule` so it's unit-tested without NATS/GPS.

## 6. State model

- Per-light: `lastPublished` state (in-memory) to detect change; cleared never persisted.
- `tz *time.Location` (resolved once / on fix). `solar` cache: `{date, sunrise, sunset}` for
  the location, recomputed at local-midnight rollover and on first fix.
- No database. No persisted overrides (deferred).

## 7. Local dev / build (no `replace`)

- A `go.work` at `/gorai-all` with `use ./gorai-tasmota ./gorai-lights` lets gorai-lights build
  against the **local** refactored gorai-tasmota while `gorai`/`gorai-gps` come from the proxy
  (published tags). `go.work` is a dev artifact (gitignored), not a `replace` directive.
- Delivery: gorai-tasmota gets a new tag; gorai-lights pins it; go.work removed. (Push/tag
  needs the maintainer's credentials.)

## 8. External dependencies (new)

- `github.com/nathan-osman/go-sunrise` — pure-Go sunrise/sunset (lat,lng,date → UTC). MIT.
- `github.com/bradfitz/latlong` — embedded lat/long → IANA zone (~80 KB), offline.

## 9. Error handling & safety (maps to Go rules)

- All device HTTP via the device package's `*http.Client` with a 10s timeout (CWE-400).
- No `InsecureSkipVerify`; LAN HTTP only (devices are plain HTTP — documented; no secrets sent).
- Controller validates/clamps command payloads at the boundary (state∈{on,off}, brightness 0–255).
- Per-light failures isolated and logged; never crash the robot. No secrets in config or logs.
- No DB, no shell, no user file paths → SQLi/command-injection/path-traversal not applicable.
