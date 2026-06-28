# gorai-lights — Requirements

**Status:** Example robot (reference implementation)

> North star: [../gorai/VISION.md](../gorai/VISION.md). gorai-lights is the smallest
> end-to-end demonstration of NCP (resources + tools + events over NATS) and the
> Composite Robot (one logical robot composed of independent binaries on a mesh).

## 1. Purpose

Demonstrate an agent that perceives the world through an NCP **resource** (a GPS
sensor providing accurate time) and acts on it through an NCP **tool** (a Tasmota
light), with an **event** path for immediate control. Keep it minimal but real.

## 2. Functional requirements

- **REQ-1 — GPS-disciplined time.** The robot MUST derive UTC time from the GPS
  resource stream (`gorai.<namespace>.<gps>.data`) by parsing `$GPRMC` (date+time)
  or `$GPGGA` (time-of-day) sentences, accurate to within one second of GPS time.
  Until the first fix, the schedule MUST pause (not fire against an undisciplined clock).
- **REQ-2 — Scheduled control.** At `on_time` the robot MUST invoke the light tool
  with `{"state":"on"}`; at `off_time`, `{"state":"off"}`. Each edge fires at most
  once per UTC day. Times are UTC `HH:MM`; either may be omitted to disable that edge.
- **REQ-3 — Event control.** A message on the configured event subject
  (`gorai.<namespace>.<on_event>.event`) MUST turn the light on immediately.
- **REQ-4 — Tool invocation.** The light is invoked as an NCP tool on
  `gorai.<namespace>.tasmota.<light_device>.command`. The robot MUST NOT drive
  hardware directly; it only calls the tool on the mesh.
- **REQ-5 — No hardware required to run.** With the GPS device set to `/dev/gps-sim`,
  the built-in simulator MUST let the robot run end-to-end without GPS hardware.
- **REQ-6 — Base robot.** `robot.json` MUST declare the GPS component and the
  scheduler service as the base robot, with `discovery.enabled: false` by default.

## 3. Non-functional / boundaries

- **REQ-7 — Safety at the tool.** This robot issues intent only. Rate-limiting,
  interlocks, and clamping for the light belong in the Tasmota capability node (per
  the gorai-tasmota requirements), never here.
- **REQ-8 — Composable.** The Tasmota light is a separate binary on the mesh; the
  robot MUST address it by subject, not by linking it in. Adding more tools (a button
  that emits the turn-on event) or resources (lux/motion sensors) MUST NOT require
  changing the scheduler service.

## 4. Known integration gap

The light only switches when a `gorai-tasmota` capability node is running under the
same namespace and subscribed to its NCP tool subject
(`gorai.<namespace>.tasmota.<device>.command`). That tool surface is specified in the
gorai-tasmota requirements. Until a node is attached, the scheduler runs and logs the
commands it would send.

## 5. Out of scope (for now)

- A physical button component that publishes the turn-on event (added later).
- Additional sensors/resources and reactions to them (added later).
- Per-timezone scheduling (UTC only), brightness/color control, multiple lights.
