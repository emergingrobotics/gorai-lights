// Package timezones holds the every-timezone matrix test (REQUIREMENTS §11.5,
// REQ-TEST-11..13). For each coordinate it boots the REAL scheduler wired to the
// GPS injector and command recorder, injects a fix at that location, and asserts
// the scheduler (a) derives the expected IANA zone and (b) therefore fires the
// expected command at the expected LOCAL time — observed on the command subject.
//
// A clock window is used as the zone discriminator: the same absolute instant
// maps to a different wall-clock time in each zone, so an "on" at 12:30 local
// proves the derived zone (not UTC) drove the decision. DST is handled by the
// standard library because comparisons use the resolved *time.Location.
package timezones

import (
	"testing"
	"time"

	"github.com/emergingrobotics/gorai/pkg/registry"

	"github.com/emergingrobotics/gorai-lights/internal/tz"
	"github.com/emergingrobotics/gorai-lights/test/harness"
)

const (
	device    = "porch"
	namespace = "tztest"
	waitFor   = 3 * time.Second
)

// injectInstant is an arbitrary fixed time encoded into the injected fix; the
// scheduler ignores GPS time and uses the instant passed to EvaluateOnce.
var injectInstant = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// clockWindowAttrs schedules one light ON from 12:00 to 13:00 local, with the
// timezone derived from GPS position (no override).
func clockWindowAttrs() registry.Config {
	return registry.Config{
		"lights": []any{
			map[string]any{"name": device, "device": device, "schedule": []any{
				map[string]any{"on": "12:00", "off": "13:00"},
			}},
		},
	}
}

func overrideAttrs(zone string) registry.Config {
	c := clockWindowAttrs()
	c["timezone"] = zone
	return c
}

func allDayAttrs() registry.Config {
	return registry.Config{
		"lights": []any{
			map[string]any{"name": device, "device": device, "schedule": []any{
				map[string]any{"on": "00:00", "off": "24:00"},
			}},
		},
	}
}

// TEST-PLAN / REQ-TEST-11: every timezone in the matrix derives the expected
// IANA zone and fires on/off at the expected local time.
func TestEveryTimezone(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		zone     string
	}{
		{"Los Angeles", 37.7749, -122.4194, "America/Los_Angeles"},
		{"Anchorage", 61.2181, -149.9003, "America/Anchorage"},
		{"Honolulu", 21.3069, -157.8583, "Pacific/Honolulu"},
		{"Denver", 39.7392, -104.9903, "America/Denver"},
		{"Chicago", 41.8781, -87.6298, "America/Chicago"},
		{"New York", 40.7128, -74.0060, "America/New_York"},
		{"Sao Paulo", -23.55, -46.63, "America/Sao_Paulo"},
		{"London", 51.5074, -0.1278, "Europe/London"},
		{"Paris", 48.8566, 2.3522, "Europe/Paris"},
		{"Moscow", 55.7558, 37.6173, "Europe/Moscow"},
		{"Johannesburg", -26.2041, 28.0473, "Africa/Johannesburg"},
		{"Dubai", 25.2048, 55.2708, "Asia/Dubai"},
		{"Kolkata (UTC+5:30)", 22.5726, 88.3639, "Asia/Kolkata"},
		{"Shanghai", 31.2304, 121.4737, "Asia/Shanghai"},
		{"Tokyo", 35.6762, 139.6503, "Asia/Tokyo"},
		{"Sydney", -33.8688, 151.2093, "Australia/Sydney"},
		{"Auckland (dateline)", -36.8485, 174.7633, "Pacific/Auckland"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Direct zone correctness (REQ-TZ-1).
			if got, ok := tz.Lookup(c.lat, c.lng); !ok || got != c.zone {
				t.Fatalf("tz.Lookup(%v,%v) = %q,%v; want %q", c.lat, c.lng, got, ok, c.zone)
			}

			loc, err := time.LoadLocation(c.zone)
			if err != nil {
				t.Fatalf("load %q: %v", c.zone, err)
			}

			h := harness.New(t, namespace, "gps", clockWindowAttrs())
			h.InjectAndSettle(t, c.lat, c.lng, injectInstant)

			// 12:30 local falls inside the 12:00–13:00 window only if the
			// scheduler honors the GPS-derived zone.
			noonLocal := time.Date(2026, 6, 1, 12, 30, 0, 0, loc)
			h.Scheduler.EvaluateOnce(noonLocal, false)
			if _, ok := h.Recorder.WaitFor(device, "on", waitFor); !ok {
				t.Fatalf("%s: expected ON at 12:30 local (zone %s)", c.name, c.zone)
			}

			// 03:00 local is outside the window → OFF.
			earlyLocal := time.Date(2026, 6, 1, 3, 0, 0, 0, loc)
			h.Scheduler.EvaluateOnce(earlyLocal, false)
			if _, ok := h.Recorder.WaitFor(device, "off", waitFor); !ok {
				t.Fatalf("%s: expected OFF at 03:00 local (zone %s)", c.name, c.zone)
			}
		})
	}
}

// REQ-TEST-12: a clock window resolves correctly on both sides of a DST
// transition, in both hemispheres. New York is EST (UTC-5) in January and EDT
// (UTC-4) in July; Sydney is reversed — AEDT (UTC+11) in January and AEST
// (UTC+10) in July. 12:30 local is inside the 12:00–13:00 window in every case,
// despite the differing UTC offset.
func TestDSTBothSides(t *testing.T) {
	cases := []struct {
		label    string
		lat, lng float64
		date     time.Time
	}{
		{"New York winter (EST)", 40.7128, -74.0060, time.Date(2026, 1, 15, 12, 30, 0, 0, mustLoc(t, "America/New_York"))},
		{"New York summer (EDT)", 40.7128, -74.0060, time.Date(2026, 7, 15, 12, 30, 0, 0, mustLoc(t, "America/New_York"))},
		{"Sydney summer (AEDT)", -33.8688, 151.2093, time.Date(2026, 1, 15, 12, 30, 0, 0, mustLoc(t, "Australia/Sydney"))},
		{"Sydney winter (AEST)", -33.8688, 151.2093, time.Date(2026, 7, 15, 12, 30, 0, 0, mustLoc(t, "Australia/Sydney"))},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			h := harness.New(t, namespace, "gps", clockWindowAttrs())
			h.InjectAndSettle(t, c.lat, c.lng, injectInstant)
			h.Scheduler.EvaluateOnce(c.date, false)
			if _, ok := h.Recorder.WaitFor(device, "on", waitFor); !ok {
				t.Fatalf("%s: expected ON at 12:30 local", c.label)
			}
		})
	}
}

func mustLoc(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatalf("load %q: %v", zone, err)
	}
	return loc
}

// REQ-TEST-13: open-ocean coordinates match no zone and fall back to UTC; the
// robot does not fail. With UTC in effect, 12:30 UTC is inside the window.
func TestOceanFallbackToUTC(t *testing.T) {
	const lat, lng = 0.0, -140.0 // mid-Pacific
	if _, ok := tz.Lookup(lat, lng); ok {
		t.Fatalf("expected no zone for mid-Pacific (%v,%v)", lat, lng)
	}

	h := harness.New(t, namespace, "gps", clockWindowAttrs())
	h.InjectAndSettle(t, lat, lng, injectInstant)

	noonUTC := time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC)
	h.Scheduler.EvaluateOnce(noonUTC, false)
	if _, ok := h.Recorder.WaitFor(device, "on", waitFor); !ok {
		t.Fatal("expected ON at 12:30 UTC under the UTC fallback")
	}
}

// REQ-TEST-13: an explicit timezone override wins over the GPS position, even
// over an ocean coordinate that resolves to no zone.
func TestOverrideWinsOverPosition(t *testing.T) {
	const lat, lng, zone = 0.0, -140.0, "Asia/Kolkata"
	loc, _ := time.LoadLocation(zone)

	h := harness.New(t, namespace, "gps", overrideAttrs(zone))
	h.InjectAndSettle(t, lat, lng, injectInstant)

	noonLocal := time.Date(2026, 6, 1, 12, 30, 0, 0, loc)
	h.Scheduler.EvaluateOnce(noonLocal, false)
	if _, ok := h.Recorder.WaitFor(device, "on", waitFor); !ok {
		t.Fatalf("expected ON at 12:30 %s under override", zone)
	}
}

// REQ-TEST-13: a polar coordinate must not crash the robot. An all-day window is
// ON regardless of which zone (if any) resolves, so the assertion is robust.
func TestPolarCoordinateDoesNotCrash(t *testing.T) {
	const lat, lng = 89.9, 0.0 // near the north pole

	h := harness.New(t, namespace, "gps", allDayAttrs())
	h.InjectAndSettle(t, lat, lng, injectInstant)

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	h.Scheduler.EvaluateOnce(now, false)
	if _, ok := h.Recorder.WaitFor(device, "on", waitFor); !ok {
		t.Fatal("expected ON for an all-day window at a polar coordinate")
	}
}
