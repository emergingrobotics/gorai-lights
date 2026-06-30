package schedule

import (
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return loc
}

// TEST-PLAN B1: time-spec parsing.
func TestParseTimeSpec(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		check   func(TimeSpec) bool
	}{
		{in: "18:30", check: func(ts TimeSpec) bool { return ts.Kind == Clock && ts.Hour == 18 && ts.Minute == 30 }},
		{in: "07:05", check: func(ts TimeSpec) bool { return ts.Kind == Clock && ts.Hour == 7 && ts.Minute == 5 }},
		{in: "24:00", check: func(ts TimeSpec) bool { return ts.Kind == Clock && ts.Hour == 24 && ts.Minute == 0 }},
		{in: "sunset", check: func(ts TimeSpec) bool { return ts.Kind == Sunset && ts.Offset == 0 }},
		{in: "sunrise", check: func(ts TimeSpec) bool { return ts.Kind == Sunrise && ts.Offset == 0 }},
		{in: "sunset-00:30", check: func(ts TimeSpec) bool { return ts.Kind == Sunset && ts.Offset == -30*time.Minute }},
		{in: "sunrise+00:10", check: func(ts TimeSpec) bool { return ts.Kind == Sunrise && ts.Offset == 10*time.Minute }},
		{in: "SUNSET+01:00", check: func(ts TimeSpec) bool { return ts.Kind == Sunset && ts.Offset == time.Hour }},
		{in: "7:5", wantErr: true},
		{in: "25:00", wantErr: true},
		{in: "18:60", wantErr: true},
		{in: "24:30", wantErr: true},
		{in: "sunset~01:00", wantErr: true},
		{in: "sunset-99:99", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		ts, err := ParseTimeSpec(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseTimeSpec(%q): expected error, got %+v", c.in, ts)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTimeSpec(%q): unexpected error: %v", c.in, err)
			continue
		}
		if !c.check(ts) {
			t.Errorf("ParseTimeSpec(%q): unexpected result %+v", c.in, ts)
		}
	}
}

func mustSpec(t *testing.T, s string) TimeSpec {
	t.Helper()
	ts, err := ParseTimeSpec(s)
	if err != nil {
		t.Fatalf("ParseTimeSpec(%q): %v", s, err)
	}
	return ts
}

// TEST-PLAN B2: static window validation (no wrap).
func TestWindowValidateStatic(t *testing.T) {
	ok := Window{On: mustSpec(t, "18:30"), Off: mustSpec(t, "23:00")}
	if err := ok.ValidateStatic(); err != nil {
		t.Errorf("valid clock window rejected: %v", err)
	}
	equal := Window{On: mustSpec(t, "18:00"), Off: mustSpec(t, "18:00")}
	if err := equal.ValidateStatic(); err == nil {
		t.Error("on==off window should be rejected")
	}
	wrap := Window{On: mustSpec(t, "22:00"), Off: mustSpec(t, "06:00")}
	if err := wrap.ValidateStatic(); err == nil {
		t.Error("off<on window should be rejected (no midnight wrap)")
	}
	// A solar window cannot be fully validated statically; it must pass.
	solar := Window{On: mustSpec(t, "sunset"), Off: mustSpec(t, "23:00")}
	if err := solar.ValidateStatic(); err != nil {
		t.Errorf("solar window should pass static validation: %v", err)
	}
}

// TEST-PLAN B3: desired-state membership, boundaries, multiple/overlapping windows.
func TestDesiredStateClockWindows(t *testing.T) {
	loc := mustLoad(t, "America/Los_Angeles")
	at := func(h, m int) time.Time { return time.Date(2026, 3, 10, h, m, 0, 0, loc) }
	windows := []Window{{On: mustSpec(t, "18:30"), Off: mustSpec(t, "23:00")}}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before on", at(18, 29), false},
		{"at on edge (inclusive)", at(18, 30), true},
		{"inside", at(20, 0), true},
		{"at off edge (exclusive)", at(23, 0), false},
		{"after off", at(23, 1), false},
	}
	for _, c := range cases {
		on, deferred, err := DesiredState(c.now, windows, loc, time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", c.name, err)
		}
		if deferred {
			t.Fatalf("%s: clock window should not defer", c.name)
		}
		if on != c.want {
			t.Errorf("%s: got on=%v want %v", c.name, on, c.want)
		}
	}
}

func TestDesiredStateMultipleAndOverlapping(t *testing.T) {
	loc := mustLoad(t, "America/Los_Angeles")
	// Two windows; the gap between them is OFF, each window is ON, and an
	// overlapping pair unions to a single ON span (REQ-CFG-7).
	windows := []Window{
		{On: mustSpec(t, "06:00"), Off: mustSpec(t, "09:00")},
		{On: mustSpec(t, "08:00"), Off: mustSpec(t, "10:00")},
		{On: mustSpec(t, "20:00"), Off: mustSpec(t, "22:00")},
	}
	at := func(h, m int) time.Time { return time.Date(2026, 6, 1, h, m, 0, 0, loc) }
	check := func(now time.Time, want bool) {
		on, _, err := DesiredState(now, windows, loc, time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if on != want {
			t.Errorf("at %s: got %v want %v", now.Format("15:04"), on, want)
		}
	}
	check(at(7, 0), true)   // first window
	check(at(9, 30), true)  // inside the overlap union (second window)
	check(at(10, 0), false) // both morning windows ended
	check(at(12, 0), false) // gap
	check(at(21, 0), true)  // evening window
}

// Two same-day windows reproduce overnight coverage without a midnight wrap.
func TestDesiredStateNoWrapTwoWindows(t *testing.T) {
	loc := mustLoad(t, "UTC")
	windows := []Window{
		{On: mustSpec(t, "22:00"), Off: mustSpec(t, "24:00")},
		{On: mustSpec(t, "00:00"), Off: mustSpec(t, "06:00")},
	}
	at := func(h, m int) time.Time { return time.Date(2026, 1, 15, h, m, 0, 0, loc) }
	check := func(now time.Time, want bool) {
		on, _, err := DesiredState(now, windows, loc, time.Time{}, time.Time{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if on != want {
			t.Errorf("at %s: got %v want %v", now.Format("15:04"), on, want)
		}
	}
	check(at(23, 0), true) // late evening window
	check(at(2, 0), true)  // early morning window
	check(at(12, 0), false)
	check(at(6, 0), false) // off edge exclusive
}

// TEST-PLAN B3: solar windows resolve against injected sunrise/sunset with offsets.
func TestDesiredStateSolarWindow(t *testing.T) {
	loc := mustLoad(t, "America/Los_Angeles")
	sunset := time.Date(2026, 6, 1, 20, 15, 0, 0, loc)
	sunrise := time.Date(2026, 6, 1, 5, 47, 0, 0, loc)
	// On 15 min before sunset, off at 23:30.
	windows := []Window{{On: mustSpec(t, "sunset-00:15"), Off: mustSpec(t, "23:30")}}

	before := time.Date(2026, 6, 1, 19, 59, 0, 0, loc) // before sunset-15m (20:00)
	on, deferred, err := DesiredState(before, windows, loc, sunrise, sunset)
	if err != nil || deferred {
		t.Fatalf("before: err=%v deferred=%v", err, deferred)
	}
	if on {
		t.Error("should be off before sunset-15m")
	}
	after := time.Date(2026, 6, 1, 20, 5, 0, 0, loc) // after 20:00 trigger
	on, _, err = DesiredState(after, windows, loc, sunrise, sunset)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Error("should be on after sunset-15m")
	}
}

// REQ-SOLAR-4: a solar window with no solar times available defers; a clock
// window in the same set still evaluates.
func TestDesiredStateDefersWhenSolarMissing(t *testing.T) {
	loc := mustLoad(t, "UTC")
	windows := []Window{
		{On: mustSpec(t, "sunset"), Off: mustSpec(t, "23:00")},
		{On: mustSpec(t, "06:00"), Off: mustSpec(t, "08:00")},
	}
	now := time.Date(2026, 1, 15, 7, 0, 0, 0, loc) // inside the clock window
	on, deferred, err := DesiredState(now, windows, loc, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !deferred {
		t.Error("solar window with no solar times should defer")
	}
	if !on {
		t.Error("clock window should still drive the light on")
	}
}

// REQ-TIME-1 / TEST-PLAN B3: a clock window resolves correctly across a DST
// spring-forward day. On 2026-03-08 US/Pacific skips 02:00–03:00; a 23:00 off
// boundary is unaffected and 20:00–23:00 still behaves as a 3-hour window.
func TestDesiredStateDSTDay(t *testing.T) {
	loc := mustLoad(t, "America/Los_Angeles")
	windows := []Window{{On: mustSpec(t, "20:00"), Off: mustSpec(t, "23:00")}}
	on, _, err := DesiredState(time.Date(2026, 3, 8, 21, 30, 0, 0, loc), windows, loc, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Error("evening window should be ON on the DST spring-forward day")
	}
	off, _, err := DesiredState(time.Date(2026, 3, 8, 23, 30, 0, 0, loc), windows, loc, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if off {
		t.Error("should be OFF after 23:00 on the DST day")
	}
}

// A solar window that resolves to off<=on is a no-wrap violation surfaced as an
// error, and must not force the light on (REQ-CFG-6).
func TestDesiredStateSolarNoWrapViolation(t *testing.T) {
	loc := mustLoad(t, "UTC")
	sunset := time.Date(2026, 6, 1, 20, 0, 0, 0, loc)
	// off (19:00) is before on (sunset=20:00): invalid window.
	windows := []Window{{On: mustSpec(t, "sunset"), Off: mustSpec(t, "19:00")}}
	now := time.Date(2026, 6, 1, 21, 0, 0, 0, loc)
	on, _, err := DesiredState(now, windows, loc, time.Time{}, sunset)
	if err == nil {
		t.Error("expected a no-wrap resolution error")
	}
	if on {
		t.Error("an invalid window must not force the light on")
	}
}
