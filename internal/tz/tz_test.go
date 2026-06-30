package tz

import (
	"testing"
)

// TEST-PLAN C2: known coordinates resolve to the expected IANA zone.
func TestLookupKnownCoordinates(t *testing.T) {
	cases := []struct {
		name     string
		lat, lng float64
		want     string
	}{
		{"San Francisco", 37.7749, -122.4194, "America/Los_Angeles"},
		{"New York", 40.7128, -74.0060, "America/New_York"},
		{"London", 51.5074, -0.1278, "Europe/London"},
		{"Tokyo", 35.6762, 139.6503, "Asia/Tokyo"},
		{"Sydney", -33.8688, 151.2093, "Australia/Sydney"},
	}
	for _, c := range cases {
		got, ok := Lookup(c.lat, c.lng)
		if !ok {
			t.Errorf("%s: no zone found", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// REQ-TZ-5: open-ocean coordinates match no zone.
func TestLookupNoMatch(t *testing.T) {
	if zone, ok := Lookup(0.0, -140.0); ok {
		t.Errorf("expected no zone for mid-Pacific, got %q", zone)
	}
}

// REQ-TZ-2: precedence override → lookup → UTC.
func TestResolvePrecedence(t *testing.T) {
	// Override wins even when a valid fix is present.
	r := Resolve("America/New_York", 37.7749, -122.4194, true)
	if r.Zone != "America/New_York" || r.Degraded {
		t.Errorf("override should win: %+v", r)
	}

	// No override, valid fix → GPS lookup.
	r = Resolve("", 35.6762, 139.6503, true)
	if r.Zone != "Asia/Tokyo" || r.Degraded {
		t.Errorf("expected GPS lookup to Asia/Tokyo: %+v", r)
	}

	// No override, no fix → degraded UTC fallback.
	r = Resolve("", 0, 0, false)
	if r.Zone != "UTC" || !r.Degraded {
		t.Errorf("expected degraded UTC fallback: %+v", r)
	}

	// No override, fix present but no matching zone → degraded UTC fallback.
	r = Resolve("", 0.0, -140.0, true)
	if r.Zone != "UTC" || !r.Degraded {
		t.Errorf("expected degraded UTC fallback for ocean: %+v", r)
	}

	// An invalid override falls through to the GPS lookup.
	r = Resolve("Not/AZone", 51.5074, -0.1278, true)
	if r.Zone != "Europe/London" || r.Degraded {
		t.Errorf("invalid override should fall through to lookup: %+v", r)
	}
}
