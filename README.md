# gorai-lights

A tiny, complete [Gorai](../gorai/VISION.md) example robot. It keeps accurate
time from a **GPS resource** and switches a **Tasmota light tool** on and off at
prescribed times — or the moment a "turn light on" **event** arrives.

Disclaimer: This works for me — that's the entire guarantee. Built with AI in the loop, so check your own biases before you love it or hate it on principle. Use at your own risk, fork freely, and don't @ me when it explodes. (But do drop me a note if it helps — pay it forward.)

## What it shows

This is the smallest honest demonstration of the platform's north star — see
**[VISION.md](../gorai/VISION.md)**: a robot is a set of **capabilities on a NATS
mesh** (NCP, the NATS Capability Protocol). Nothing here is wired by hand; the
pieces find each other by NATS subject.

| Role | NCP primitive | Subject |
|------|---------------|---------|
| GPS receiver (time source) | **resource** (sensor) | reads `gorai.<ns>.gps.data` |
| Tasmota light | **tool** (actuator) | calls `gorai.<ns>.tasmota.porch.command` |
| "turn light on" signal | **event** | reacts to `gorai.<ns>.button.event` |

`<ns>` is the subject namespace the runtime uses (currently `gorai`).

### The logic

1. The **GPS component** (from [`gorai-gps`](../gorai-gps)) streams NMEA sentences.
   A built-in simulator runs when the device is `/dev/gps-sim`, so no hardware is
   needed to try it.
2. The **lights scheduler** (this repo, `services/lights`) parses each `$GPRMC`/`$GPGGA`
   sentence for **UTC time**, disciplines its clock to GPS (accurate to the second),
   and once per second checks the schedule.
3. At `on_time` it invokes the light tool with `{"state":"on"}`; at `off_time`,
   `{"state":"off"}`. A message on the event subject turns the light on immediately.

> **Times are UTC** (GPS time is UTC). `on_time: "18:30"` means 18:30 UTC.

## The Composite Robot in miniature

The light itself is **not** part of this binary. It is a tool exposed on the mesh
by a separate [`gorai-tasmota`](../gorai-tasmota) capability node, running under the
same robot namespace. Two binaries, one logical robot — composed at runtime through
the mesh. That is the [Composite Robot](../gorai/VISION.md) at its smallest.

To actually switch a bulb, run a `gorai-tasmota` service that subscribes to its NCP
tool subject (`gorai.<ns>.tasmota.<device>.command`) and drives the device. That tool
surface is specified in the gorai-tasmota requirements; until a node is attached, the
scheduler still runs and logs every command it sends (publish is fire-and-forget, so
nothing blocks).

## Run it

```bash
# Compile (plain Go -- no gorai CLI required)
make build        # or: go build ./...

# Run with the gorai CLI (embedded NATS, GPS simulator)
make run          # gorai run robot.json
```

Watch the logs: the scheduler reports the GPS-disciplined clock and every light
command it emits. Publish a manual turn-on event to see the event path:

```bash
nats pub gorai.gorai.button.event '{}'
```

## Configuration (`robot.json`)

The **base robot** is the GPS component + the scheduler service. The scheduler's
attributes:

| Attribute | Meaning |
|-----------|---------|
| `gps` | name of the GPS component to read time from (`gps`) |
| `light_device` | Tasmota device name → tool subject `…tasmota.<device>.command` |
| `on_time` / `off_time` | `HH:MM` UTC; omit either to disable that edge |
| `on_event` | event source name → subject `…<source>.event` (turns light on) |

`discovery.enabled` is `false`: this robot is exactly what `robot.json` declares.
Set it to `true` to let the robot adopt additional tools/resources discovered on the
mesh (and authorized by its NATS credentials) — see the
[RDL discovery toggle](../gorai-docs/docs/specifications/robot-definition-language.md).

## Extending it (later)

The design is deliberately open at both ends:

- **More tools** — add a physical **button** as a component that publishes to
  `gorai.<ns>.button.event`; the scheduler already listens and will turn the light on.
- **More resources** — add sensors (lux, motion, temperature) and have the scheduler
  (or another agent) react to their readings/events.

None of those require changing this service — they are new capabilities on the same
mesh. That is the whole point.
