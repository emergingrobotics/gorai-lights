# gorai-lights

A small, complete [Gorai](../gorai/VISION.md) example robot: a single binary that reads its
**GPS position**, derives the **local timezone** and **daily sunrise/sunset** entirely offline, and
switches a **Tasmota light** on and off on a per-day schedule of clock-time and solar windows —
all over a NATS mesh.

Disclaimer: This works for me — that's the entire guarantee. Built with AI in the loop, so check your own biases before you love it or hate it on principle. Use at your own risk, fork freely, and don't @ me when it explodes. (But do drop me a note if it helps — pay it forward.)

## What it shows

The platform's north star (see **[VISION.md](../gorai/VISION.md)**): a robot is a set of
**capabilities on a NATS mesh** (NCP). Nothing is wired by hand — the pieces find each other by
NATS subject.

| Role | NCP primitive | Subject |
|------|---------------|---------|
| GPS receiver (position source) | **resource** (sensor) | reads `gorai.<ns>.gps.data` |
| Tasmota light | **tool** (actuator) | publishes `gorai.<ns>.tasmota.<device>.command` |

`<ns>` is the robot's namespace — it defaults to the robot name, so here it is `lights`
(subjects are `gorai.lights.…`).

### The logic (v3)

1. The **GPS component** ([`gorai-gps`](../gorai-gps)) streams NMEA. A built-in simulator runs when
   the device is `/dev/gps-sim`, so no hardware is needed to try it.
2. The **lights scheduler** (`services/lights`) parses `$GPRMC`/`$GPGGA` for **latitude/longitude**
   and:
   - resolves the **IANA timezone** from position via an embedded offline table
     (`bradfitz/latlong`) — override with `timezone`, falls back to UTC;
   - computes **sunrise/sunset** locally (`nathan-osman/go-sunrise`);
   - evaluates each light's **windows** and, on a state change (or on the re-assert tick), publishes
     `{"state":"on"|"off"}` to the light's command subject.
3. Wall time is the **system clock** (assume NTP/RTC); GPS supplies *position*, not the clock.

> **Control is a NATS command.** The scheduler never touches a bulb — it publishes a command that a
> Tasmota capability node (or the built-in simulator) consumes. Any other component on the mesh may
> publish the same command (a button, a dashboard, a remote operator).

## Schedule windows

Each light has one or more `{ "on": <timespec>, "off": <timespec> }` windows. The light is ON
whenever now falls inside any window. A `<timespec>` is:

- `"HH:MM"` — wall-clock in the resolved timezone (`"24:00"` allowed only as an `off` boundary);
- `"sunrise"` / `"sunset"` — today's solar event at the robot's location;
- `"sunrise±HH:MM"` / `"sunset±HH:MM"` — solar event with a signed offset.

Windows **do not cross midnight** (REQ-CFG-6): the resolved `on` must be before the resolved `off`.
For overnight coverage use two windows (`22:00`–`24:00` and `00:00`–`06:00`).

## Run it

The binary registers **both** Tasmota drivers; the config picks one:

| Config | GPS | Tasmota driver |
|--------|-----|----------------|
| `robot.json` (`make build` / `make run`) | real serial (`/dev/ttyUSB0`) | **real** `gorai-tasmota` HTTP controller — drives Sonoff/Kauf bulbs |
| `robot.test.json` (`make run-test`) | simulator (`/dev/gps-sim`) | **simulated** in-process light — fully self-contained, no hardware |

```bash
make build            # the real robot: links gorai-tasmota's HTTP controller
make run              # run robot.json (drives real bulbs over the LAN)
make run-test         # fully simulated end-to-end — no hardware, no external services
```

> **Run *this* binary, not the global `gorai` CLI** — components live in the binary that
> blank-imports them (the Caddy model). The Make targets invoke `./bin/gorai-lights`.
>
> **Build note:** `make build` links the [`gorai-tasmota`](../gorai-tasmota) HTTP controller, pulled
> as a normal tagged module dependency (`v0.1.0`) from the Go proxy (see [docs/DESIGN.md](docs/DESIGN.md)
> §7). `make run-test` produces the same binary and selects the simulator through the config.

Watch `make run-test` and you will see the timezone resolve from the simulated GPS position
(Golden Gate → `America/Los_Angeles`), the day's sunrise/sunset computed, and each light command as
it is published and consumed by the simulated light. With `robot.json`, the `tasmota/controller`
service receives the same commands and issues the HTTP calls to the configured device addresses
(per-device failures are logged and isolated — one unreachable bulb never blocks the others).

## Configuration

The scheduler service attributes (see `robot.json`):

| Attribute | Meaning |
|-----------|---------|
| `gps` | name of the GPS component to read position from (default `gps`) |
| `timezone` | optional IANA override; if omitted, derived from GPS position, else UTC |
| `location` | optional `{latitude, longitude}` fallback used until a GPS fix arrives |
| `reconcile_interval` | re-assert cadence (default `60s`) |
| `lights[]` | each `{ name, device?, schedule:[{on,off}…] }` (`device` defaults to `name`) |

## Testing

`make test` runs the full suite with **no hardware, no external NATS, and no network**:

- pure decision logic — `internal/schedule`, `internal/solar`, `internal/tz`;
- the scheduler service over embedded NATS — `services/lights`;
- the **test harness** ([REQUIREMENTS.md](REQUIREMENTS.md) §11) under `test/`:
  a GPS injector (synthetic NMEA for any lat/long/time), a NATS command recorder, an
  **every-timezone matrix** (17 zones, DST on both hemispheres, ocean/pole/override fallbacks), and
  robot fixtures that boot through the real gorai runtime.

See [docs/DESIGN.md](docs/DESIGN.md) and [docs/TEST-PLAN.md](docs/TEST-PLAN.md).

## Extending it

The design is open at both ends: add more **tools** (a button publishing a command, more lights) or
more **resources** (lux, motion, temperature) as capabilities on the same mesh — no change to this
service required.
