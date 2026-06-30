package lights

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"

	"github.com/emergingrobotics/gorai-lights/test/gpsinject"
)

type fakeDeps struct{ nc *nats.Conn }

func (d fakeDeps) Get(name string) (any, error) {
	if name == "nats" {
		return d.nc, nil
	}
	return nil, fmt.Errorf("dependency %q not provided", name)
}
func (d fakeDeps) GetByType(string) ([]any, error) { return nil, nil }

func startNATS(t *testing.T) *nats.Conn {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { nc.Close(); ns.Shutdown() })
	return nc
}

// commandRecorder collects light commands published to a subject.
type commandRecorder struct{ ch chan string }

func recordCommands(t *testing.T, nc *nats.Conn, subject string) *commandRecorder {
	t.Helper()
	r := &commandRecorder{ch: make(chan string, 32)}
	sub, err := nc.Subscribe(subject, func(m *nats.Msg) {
		var c struct {
			State string `json:"state"`
		}
		if json.Unmarshal(m.Data, &c) == nil {
			r.ch <- c.State
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	_ = nc.Flush()
	return r
}

// expect waits for the next command and asserts its state.
func (r *commandRecorder) expect(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-r.ch:
		if got != want {
			t.Fatalf("got command %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for command %q", want)
	}
}

// expectNone asserts no command arrives within a short window.
func (r *commandRecorder) expectNone(t *testing.T) {
	t.Helper()
	select {
	case got := <-r.ch:
		t.Fatalf("expected no command, got %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func newScheduler(t *testing.T, nc *nats.Conn, attrs registry.Config) *Scheduler {
	t.Helper()
	v, err := New(context.Background(), fakeDeps{nc: nc}, attrs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return v.(*Scheduler)
}

func clockLightConfig(tzOverride string) registry.Config {
	return registry.Config{
		"namespace": "lights",
		"timezone":  tzOverride,
		"lights": []any{
			map[string]any{
				"name":   "porch",
				"device": "porch",
				"schedule": []any{
					map[string]any{"on": "18:30", "off": "23:00"},
				},
			},
		},
	}
}

// TEST-PLAN D1: invalid configuration fails construction with a clear error.
func TestNewRejectsInvalidConfig(t *testing.T) {
	nc := startNATS(t)
	cases := []struct {
		name  string
		attrs registry.Config
	}{
		{"empty lights", registry.Config{"lights": []any{}}},
		{"no schedule", registry.Config{"lights": []any{
			map[string]any{"name": "porch", "schedule": []any{}}}}},
		{"off before on", registry.Config{"lights": []any{
			map[string]any{"name": "porch", "schedule": []any{
				map[string]any{"on": "23:00", "off": "06:00"}}}}}},
		{"bad timespec", registry.Config{"lights": []any{
			map[string]any{"name": "porch", "schedule": []any{
				map[string]any{"on": "25:00", "off": "26:00"}}}}}},
		{"bad reconcile", registry.Config{"reconcile_interval": "nope", "lights": []any{
			map[string]any{"name": "porch", "schedule": []any{
				map[string]any{"on": "18:00", "off": "19:00"}}}}}},
		{"location out of range", registry.Config{
			"location": map[string]any{"latitude": 999.0, "longitude": 0.0},
			"lights": []any{map[string]any{"name": "porch", "schedule": []any{
				map[string]any{"on": "18:00", "off": "19:00"}}}}}},
	}
	for _, c := range cases {
		if _, err := New(context.Background(), fakeDeps{nc: nc}, c.attrs); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

// TEST-PLAN D2: a clock window drives on/off on state change, suppresses
// no-change, publishes on startup, and re-asserts on the reconcile tick.
func TestEvaluateClockWindow(t *testing.T) {
	nc := startNATS(t)
	s := newScheduler(t, nc, clockLightConfig("America/Los_Angeles"))
	rec := recordCommands(t, nc, "gorai.lights.tasmota.porch.command")

	loc, _ := time.LoadLocation("America/Los_Angeles")
	inside := time.Date(2026, 6, 1, 20, 0, 0, 0, loc)
	afterOff := time.Date(2026, 6, 1, 23, 30, 0, 0, loc)

	// Startup inside the window publishes ON.
	s.evaluate(inside, false)
	rec.expect(t, "on")

	// No state change → no publish (reassert=false).
	s.evaluate(inside.Add(time.Minute), false)
	rec.expectNone(t)

	// Crossing the off boundary publishes OFF.
	s.evaluate(afterOff, false)
	rec.expect(t, "off")

	// Reconcile tick re-asserts the current (off) state even with no change.
	s.evaluate(afterOff.Add(time.Minute), true)
	rec.expect(t, "off")
}

// TEST-PLAN D3: with no GPS fix and no location, a solar-only light defers (no
// command), while a clock light in the same robot still publishes.
func TestEvaluateDefersSolarWithoutPosition(t *testing.T) {
	nc := startNATS(t)
	attrs := registry.Config{
		"namespace": "lights",
		// No timezone override and no location: tz is the degraded UTC fallback
		// and there is no position for solar.
		"lights": []any{
			map[string]any{"name": "solar", "device": "solardev", "schedule": []any{
				map[string]any{"on": "sunset", "off": "23:00"}}},
			map[string]any{"name": "clock", "device": "clockdev", "schedule": []any{
				map[string]any{"on": "06:00", "off": "08:00"}}},
		},
	}
	s := newScheduler(t, nc, attrs)
	solarRec := recordCommands(t, nc, "gorai.lights.tasmota.solardev.command")
	clockRec := recordCommands(t, nc, "gorai.lights.tasmota.clockdev.command")

	// 07:00 UTC: clock window active, solar window deferred.
	now := time.Date(2026, 1, 15, 7, 0, 0, 0, time.UTC)
	s.evaluate(now, false)

	clockRec.expect(t, "on")
	solarRec.expectNone(t)
}

// A configured location lets solar windows resolve even with no live GPS fix
// (REQ-SOLAR-4 fallback), and the GPS-derived timezone is used.
func TestEvaluateSolarWithLocationFallback(t *testing.T) {
	nc := startNATS(t)
	attrs := registry.Config{
		"namespace": "lights",
		"location":  map[string]any{"latitude": 37.7749, "longitude": -122.4194},
		"lights": []any{
			map[string]any{"name": "porch", "device": "porch", "schedule": []any{
				map[string]any{"on": "sunset", "off": "23:30"}}},
		},
	}
	s := newScheduler(t, nc, attrs)
	rec := recordCommands(t, nc, "gorai.lights.tasmota.porch.command")

	loc, _ := time.LoadLocation("America/Los_Angeles")
	// Well after sunset (which is ~20:30 local in June) and before 23:30.
	now := time.Date(2026, 6, 1, 22, 0, 0, 0, loc)
	s.evaluate(now, false)
	rec.expect(t, "on")
}

// REQ-SOLAR-3: when a live GPS fix supersedes the configured fallback location
// the same day, solar times are recomputed for the real position. Here the
// fallback at (0,0) has sunset before 19:00 UTC (light ON), while a live fix far
// to the west pushes sunset past 19:00 (light OFF) — proving the recompute.
func TestSolarRecomputesOnLiveFix(t *testing.T) {
	nc := startNATS(t)
	attrs := registry.Config{
		"namespace": "lights",
		"timezone":  "UTC",
		"location":  map[string]any{"latitude": 0.0, "longitude": 0.0},
		"lights": []any{
			map[string]any{"name": "porch", "device": "porch", "schedule": []any{
				map[string]any{"on": "sunset", "off": "23:00"}}},
		},
	}
	s := newScheduler(t, nc, attrs)
	rec := recordCommands(t, nc, "gorai.lights.tasmota.porch.command")

	now := time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)
	s.evaluate(now, false)
	rec.expect(t, "on")

	// Live fix at longitude -45 shifts solar noon ~3h later, so sunset moves past
	// 19:00 UTC and the light must turn OFF after the same-day recompute.
	rmc := gpsinject.BuildRMC(0.0, -45.0, now, true)
	msg, _ := json.Marshal(map[string]string{"sentence": rmc})
	s.onGPS(&nats.Msg{Data: msg})

	s.evaluate(now, false)
	rec.expect(t, "off")
}

// onGPS parses a valid RMC sentence and records the position.
func TestOnGPSParsesPosition(t *testing.T) {
	nc := startNATS(t)
	s := newScheduler(t, nc, clockLightConfig("UTC"))

	// Classic valid GPRMC: 4807.038N 01131.000E (~48.117, 11.517).
	sentence := "$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A"
	msg, _ := json.Marshal(map[string]string{"sentence": sentence})
	s.onGPS(&nats.Msg{Data: msg})

	lat, lng, have := s.Position()
	if !have {
		t.Fatal("expected a position after a valid RMC sentence")
	}
	if lat < 48.0 || lat > 48.2 || lng < 11.4 || lng > 11.6 {
		t.Errorf("unexpected position: lat=%v lng=%v", lat, lng)
	}
}

// An invalid (void) RMC fix is ignored.
func TestOnGPSIgnoresInvalidFix(t *testing.T) {
	nc := startNATS(t)
	s := newScheduler(t, nc, clockLightConfig("UTC"))

	void := "$GPRMC,123519,V,,,,,,,230394,,*XX"
	msg, _ := json.Marshal(map[string]string{"sentence": void})
	s.onGPS(&nats.Msg{Data: msg})

	if _, _, have := s.Position(); have {
		t.Error("a void fix must not set a position")
	}
}
