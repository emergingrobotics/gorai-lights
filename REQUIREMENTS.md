# gorai-lights — Requirements

**Status:** Draft v3 (supersedes the earlier GPS-time demo and the in-process-call v2)
**Kind:** Example robot — a single-binary, schedule-driven Tasmota lighting controller.

> North star: [../gorai/VISION.md](../gorai/VISION.md). `gorai-lights` is a worked
> example of a **single-binary Composite Robot**: a GPS resource, a Tasmota actuator
> capability, and a scheduler agent — all in one binary, wired by NATS, configured
> entirely by `robot.json`, with **no database and no separate processes**.

Functional equivalent of the `lightsweb` reference, minus the web UI and the SQLite
database: lights turn on/off at configured times — clock times or **sunrise/sunset
computed from the robot's GPS coordinates** — with multiple on/off windows per light
per day.

---

## 1. Purpose & Scope

### In scope
- A single Go binary (`gorai-lights`) that, from `robot.json`, controls one or more
  Tasmota lights on a daily schedule.
- Schedule times expressed as wall-clock (`HH:MM`) **or** `sunrise`/`sunset` (± offset),
  with **multiple on/off windows per light per day**, each window **within a single day
  (no midnight wrap)**.
- Sunrise/sunset and the **timezone** are derived **locally from GPS coordinates** (no
  internet, no API).
- **Control happens over NATS**: the scheduler publishes on/off **commands**; a built-in
  Tasmota capability service subscribes to those commands and drives the bulbs over HTTP.
- Because control is a NATS command, **any other gorai component or agent can issue the
  same commands**, independently of the scheduler.

### Out of scope (see §9)
- Web control UI, persistent manual-override pinning, scenes/automations, light groups,
  multi-robot/fleet, and any database.

---

## 2. Architecture

One binary, the Caddy model, declared in `robot.json`. The embedded NATS bus connects the
internal services and is reachable by the rest of the mesh.

```
gorai-lights (single binary, embedded NATS bus)
├── gorai runtime          (github.com/emergingrobotics/gorai/pkg/gorai)
├── gps component          (blank import: gorai-gps/component/nmea)   → latitude / longitude
├── tasmota controller svc (blank import: gorai-tasmota, built in)    → SUBSCRIBES to each
│                                                                        light's command subject;
│                                                                        drives the bulb over HTTP
└── lights scheduler svc   (this repo)                                → reads the schedule + solar;
                                                                         PUBLISHES on/off commands
                                                                         over NATS at trigger times
```

- **REQ-ARCH-1** `gorai-lights` MUST run as a single process — no separate Tasmota daemon,
  no external NATS, no database. Embedded NATS is the runtime's internal bus.
- **REQ-ARCH-2 (control is a NATS command)** The scheduler MUST NOT drive the device directly.
  When a schedule trigger fires, the scheduler **publishes a NATS command** to the light's
  command subject. The built-in **Tasmota controller service subscribes** to that subject and
  performs the device I/O (HTTP). Scheduler and driver are decoupled by NATS even though they
  ship in one binary.
- **REQ-ARCH-3 (open command bus)** Because turning a light on/off is just a published NATS
  message, **any other gorai component or agent — in this robot or elsewhere on the mesh — MAY
  publish the same command** to control a light, independently of and concurrently with the
  scheduler. The scheduler is one publisher among possibly many (e.g. a button handler, a
  presence sensor, a manual dashboard control, a remote operator).
- **REQ-ARCH-4 (command contract)** The light command is an NCP tool:
  - **Subject:** `gorai.<namespace>.tasmota.<device>.command`
  - **Payload (JSON):** `{ "state": "on" | "off" }`
    (optional, Kauf only: `"brightness": 0–255`, `"color": {"r","g","b"}` — omitted = use the
    device's configured defaults).
  - Fire-and-forget; idempotent (commanding `on` when already on is a no-op at the device).
- **REQ-ARCH-5** GPS coordinates MUST be obtained from the in-binary `gps` component.

---

## 3. Prerequisite: `gorai-tasmota` as an importable, NATS-subscribing capability

To be the built-in driver, `gorai-tasmota` must become a reusable gorai service (its device
code is currently under `internal/`, and it only listens for `suntimes` triggers, not per-device
commands):

- **REQ-MOD-1** `gorai-tasmota` MUST register a gorai service (via `init()` →
  `registry.RegisterService("tasmota", "controller", …)`) that, from its device config,
  **subscribes to `gorai.<namespace>.tasmota.<device>.command` for each configured device** and
  drives that device when a command arrives. This is the consumer side of REQ-ARCH-4.
- **REQ-MOD-2** The device HTTP layer (currently `internal/device`) MUST move to a **public,
  importable package** (e.g. `…/gorai-tasmota/device`) with the Sonoff (Tasmota `GET
  /cm?cmnd=POWER+{ON|OFF}`) and Kauf (ESPHome turn_on/turn_off, brightness + RGB) implementations,
  plus the public action/config types. No NATS or gorai-runtime dependency in that package.
- **REQ-MOD-3** The controller service MUST validate/clamp commands at the device boundary
  (state ∈ {on,off}; brightness 0–255; ignore unknown fields) and return/log structured outcomes
  — safety lives at the capability node, not the publisher.
- **REQ-MOD-4 (optional)** A `Device.Status(ctx) (on bool, err error)` state read (Sonoff
  `GET /cm?cmnd=Power`; Kauf ESPHome state) MAY be added so the controller can skip redundant
  writes and (future) detect drift. Not required by the scheduler in v3.
- **REQ-MOD-5** The existing suntimes-driven NATS daemon MAY remain as a separate entry point but
  MUST NOT be on `gorai-lights`' import path; `gorai-lights` blank-imports only the controller
  service + device package.

---

## 4. Configuration (`robot.json`)

Two services compose the robot: the **Tasmota controller** (owns the devices, subscribes to
their command subjects) and the **lights scheduler** (owns the schedule, publishes commands).
No external config file, no DB.

- **REQ-CFG-1** Example:

```jsonc
{
  "version": "2",
  "robot": { "name": "lights", "description": "Tasmota lighting controller" },
  "nats": { "embedded": true, "url": "nats://localhost:4222" },

  "components": [
    { "name": "gps", "type": "gps", "model": "nmea",
      "attributes": { "device": "/dev/ttyUSB0", "baud_rate": 9600 } }
  ],

  "services": [
    {
      "name": "tasmota",
      "type": "tasmota",
      "model": "controller",
      "attributes": {
        "devices": [
          { "name": "porch",  "type": "sonoff", "address": "192.168.1.50" },
          { "name": "garden", "type": "kauf",   "address": "192.168.1.51",
            "on_brightness": 200, "on_color": { "r": 255, "g": 180, "b": 0 } }
        ]
      }
    },
    {
      "name": "lights",
      "type": "scheduler",
      "model": "lights",
      "attributes": {
        "timezone": "America/Los_Angeles",   // OPTIONAL override; if omitted, derived from GPS position
        "gps": "gps",                          // GPS component to read coords from
        "reconcile_interval": "60s",           // re-assert cadence (default 60s)
        "location": { "latitude": 37.77, "longitude": -122.42 },  // OPTIONAL fallback if no GPS fix
        "lights": [
          { "name": "porch",  "device": "porch",
            "schedule": [ { "on": "sunset-00:15", "off": "23:30" },
                          { "on": "05:30",        "off": "sunrise+00:10" } ] },
          { "name": "garden", "device": "garden",
            "schedule": [ { "on": "sunset", "off": "23:00" } ] }
        ]
      }
    }
  ]
}
```

### 4.1 Light → controller mapping
- **REQ-CFG-2** The Tasmota controller's `devices[]` defines each physical device: unique `name`,
  `type` ∈ `{sonoff, kauf}`, `address` (host/IP), and (Kauf only, optional) `on_brightness`/
  `on_color` applied when turned on. The controller subscribes to
  `gorai.<namespace>.tasmota.<name>.command` for each.
- **REQ-CFG-3** Each scheduler light maps to a controller via `device` (the Tasmota device `name`).
  `device` defaults to the light's `name` if omitted. **The mapping "which controller drives which
  light" is therefore the shared device name / command subject** — the scheduler publishes to
  `…tasmota.<device>.command`, which that device's controller is subscribed to.
- **REQ-CFG-4** A `device` referenced by a light MUST exist in the controller's `devices[]`;
  startup validation fails otherwise, naming the light.

### 4.2 Schedule windows (no midnight wrap)
- **REQ-CFG-5** `schedule` is a list of **windows**, each `{ "on": <timespec>, "off": <timespec> }`.
  A light is **ON** whenever the current time falls inside any of its windows, else **OFF**.
- **REQ-CFG-6 (no wrap)** Every window MUST be **within a single day, midnight-to-midnight**:
  the resolved `on` time MUST be strictly before the resolved `off` time, both within
  `00:00`–`24:00` of the same local day. **Windows MUST NOT cross midnight.** A window whose
  `off` ≤ `on` is a configuration error (fails validation, names the light/window).
  - To keep a light on across midnight, use **two windows** — e.g. `{on:"22:00", off:"24:00"}`
    and `{on:"00:00", off:"06:00"}`.
- **REQ-CFG-7** Multiple windows per light per day MUST be supported; overlapping windows union.

### 4.3 Time-spec grammar
- **REQ-CFG-8** A `<timespec>` is one of:
  - `"HH:MM"` — wall-clock in the resolved timezone (24-hour). `"24:00"` is permitted as the
    end-of-day boundary for an `off`.
  - `"sunrise"` / `"sunset"` — today's solar event at the robot's location.
  - `"sunrise±HH:MM"` / `"sunset±HH:MM"` — solar event with a signed offset
    (`"sunset-00:30"` = 30 min before sunset; `"sunrise+00:10"` = 10 min after sunrise).
- **REQ-CFG-9** Invalid timespecs, an `off ≤ on` window, unknown device, missing `address`, or an
  empty `schedule` MUST fail validation at startup with a clear, named error.

---

## 5. Time & solar

- **REQ-TIME-1** Wall-clock comparisons use the **resolved IANA timezone** (see §5.1) via
  `time.LoadLocation`, so DST is handled automatically. `HH:MM` is interpreted in that zone.
- **REQ-TIME-2** "Now" is the **system clock** (assumed NTP/RTC-synced) in that zone. GPS time
  discipline is NOT required for v3; GPS supplies **location**, not the clock.

### 5.1 Timezone resolution (GPS position → IANA zone)
- **REQ-TZ-1** The robot MUST determine its IANA timezone by **looking up its GPS coordinates in
  an embedded position→timezone table** (a timezone-boundary dataset), so the correct zone and DST
  rules are chosen automatically from where the robot physically is — no manual config, no internet.
- **REQ-TZ-2** Precedence: (1) explicit `attributes.timezone` override; else (2) GPS-position
  lookup once a fix is valid; else (3) `UTC` fallback (logged as degraded).
- **REQ-TZ-3** The lookup table MUST be **embedded and offline** — a pure-Go boundary dataset
  (e.g. `github.com/ringsaturn/tzf` for polygon accuracy, or `github.com/bradfitz/latlong` for a
  ~80 KB city-level table) compiled into the binary. City-level accuracy near borders is fine.
- **REQ-TZ-4** Resolve on first valid fix; log `lat/long → IANA zone`. A mobile robot MAY re-resolve
  on crossing a zone boundary (optional); a fixed install resolves once.
- **REQ-TZ-5** Coordinates with no matching zone (open ocean) fall back per REQ-TZ-2 and are logged;
  the robot MUST NOT fail to start.
- **REQ-TZ-6** Until a zone is resolved, `HH:MM` windows use the fallback zone (explicit `timezone`
  or `UTC`) and begin honoring the GPS-derived zone once it resolves.

### 5.2 Sunrise / sunset
- **REQ-SOLAR-1** Sunrise/sunset MUST be **computed locally, in-process**, from latitude/longitude +
  date — no network, no external service (pure-Go solar algorithm). The robot works fully offline.
- **REQ-SOLAR-2** Latitude/longitude come from the `gps` component (`Position(ctx)`), resolving the
  component named by `attributes.gps` (default `"gps"`).
- **REQ-SOLAR-3** Recompute at least daily (local midnight) and when the GPS fix first becomes valid.
- **REQ-SOLAR-4 (no fix)** Until a valid fix (and absent `attributes.location` fallback), windows
  referencing `sunrise`/`sunset` are **deferred** (logged, not driven); `HH:MM`-only windows still
  work. No crash, no driving against unresolved times.
- **REQ-SOLAR-5** Offsets (REQ-CFG-8) are applied before comparison.
- **REQ-SOLAR-6** Polar edge cases (no sunrise/sunset that day) handled gracefully (all-day/never,
  logged), not as errors — and still subject to the no-wrap rule per local day.

---

## 6. Scheduling (publishing commands)

- **REQ-SCHED-1** The scheduler evaluates each light's windows for "now" and determines the light's
  **desired state** (on/off). It MUST publish a command (REQ-ARCH-4) when:
  - **a trigger is crossed** — desired state changes from the last evaluation (a window's `on` or
    `off` time is reached), and
  - **on startup** — the first evaluation publishes each light's current desired state, so a restart
    mid-window leaves lights correct.
- **REQ-SCHED-2 (re-assert)** On every `reconcile_interval` tick the scheduler MAY **re-publish** the
  current desired state for each light (default: enabled), so device reboots, missed messages, or
  externally-caused drift self-heal without the scheduler reading device state. The controller
  service handles idempotency (and MAY use `Device.Status`, REQ-MOD-4, to skip redundant writes).
- **REQ-SCHED-3** The scheduler MUST NOT perform device I/O itself; it only publishes NATS commands.
  All HTTP/device interaction is the controller service's responsibility (§7).
- **REQ-SCHED-4** Each published command (light, device subject, target state, trigger reason) MUST be
  **logged** (structured logs) — replacing `lightsweb`'s SQLite event table. No DB.
- **REQ-SCHED-5** Desired state for "now" is computed by resolving each window's `on`/`off` timespecs
  to absolute times for the current local day (clock → local tz; solar → today's event ± offset) and
  testing membership. With no midnight wrap (REQ-CFG-6), each window is a simple `[on, off)` interval
  within the day.

---

## 7. Device control (Tasmota controller service)

- **REQ-DEV-1** The controller service subscribes to each device's command subject (REQ-ARCH-4,
  REQ-MOD-1) and, on a command, drives the device via the public device package: Sonoff
  `GET /cm?cmnd=POWER+{ON|OFF}`, Kauf ESPHome turn_on/turn_off.
- **REQ-DEV-2** Turning a Kauf light **on** applies `on_brightness`/`on_color` (from the command
  payload if present, else the device's configured defaults). Sonoff is on/off only.
- **REQ-DEV-3** HTTP calls MUST use a bounded timeout (default 10s) and isolate per-device failures
  (one unreachable bulb MUST NOT block others or crash the robot); failures are logged and recover on
  the next command / re-assert tick.
- **REQ-DEV-4** Commands are idempotent; the controller MAY publish a result/state for observers
  (optional in v3).

---

## 8. Non-functional

- **REQ-NFR-1 Offline:** operates with no internet — timezone, solar computed locally; devices on the LAN.
- **REQ-NFR-2 Single binary / no DB:** all config in `robot.json`; state is ephemeral (recomputed) or
  logged; nothing persisted to a database.
- **REQ-NFR-3 Footprint:** runs on a Raspberry Pi / small Linux host (single-binary model).
- **REQ-NFR-4 Observability:** gorai dashboard (`:10101`) shows the robot is up; the scheduler and
  controller log commands and outcomes.
- **REQ-NFR-5 Test mode:** runnable without GPS hardware (simulator via `device: "/dev/gps-sim"`),
  and SHOULD support a `dry_run` on the controller (log intended HTTP instead of issuing it) so the
  full schedule→NATS→controller path can be exercised without real bulbs.

---

## 9. Out of scope / future

- **Web control UI** (`lightsweb`'s page); the gorai dashboard covers status, and any publisher can
  command lights via NATS (REQ-ARCH-3), so a control page could be a thin future add.
- **Persistent manual-override pinning** (detect a wall-switch flip, hold the user's choice for N
  hours). `lightsweb` uses SQLite; without a DB this would be in-memory only. Deferred; re-assert
  (REQ-SCHED-2) is the v3 drift behavior.
- **Light groups / scenes**, per-window brightness/color ramps, and astronomical events beyond
  sunrise/sunset. (GPS-derived timezone is **in scope** — §5.1.)
- **Cross-midnight windows** — explicitly excluded (REQ-CFG-6); use two same-day windows.

---

## 10. Open design decisions (resolved here; flag to change)

1. **Timezone source** → **derived from GPS position** via an embedded offline coordinate→IANA table
   (§5.1, REQ-TZ-*), with an explicit `timezone` override and `UTC` fallback.
2. **Control path** → **NATS command, not in-process call.** The scheduler publishes
   `…tasmota.<device>.command`; the built-in Tasmota controller service subscribes and drives the
   device. This decouples "decide when" from "drive the device" and lets **any** component issue the
   same command (REQ-ARCH-3). Both ride in one binary over the embedded bus.
3. **Schedule representation** → `{on, off}` **windows, midnight-to-midnight, no wrap** (REQ-CFG-6);
   overnight = two windows.
4. **Drift handling** → **re-assert** the desired state via NATS each tick (REQ-SCHED-2), not by the
   scheduler reading device state. Optional `Device.Status` (REQ-MOD-4) lets the controller skip
   redundant writes; persistent override pinning is deferred.
5. **Solar** → in-process pure-Go computation (offline), recomputed daily.
6. **GPS role** → coordinates only (for timezone + solar); wall-clock from the system/NTP clock.
7. **Config layout** → two services (`tasmota/controller` owns devices; `scheduler/lights` owns
   schedule), mapped by the shared device name / command subject.
</content>
