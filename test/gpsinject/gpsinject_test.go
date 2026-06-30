package gpsinject

import (
	"math"
	"testing"
	"time"

	"github.com/emergingrobotics/gorai-gps/pkg/gps"
)

// Generated sentences must have a valid checksum and parse back through the real
// gorai-gps parser to the injected coordinates (REQ-TEST-5).
func TestBuildSentencesRoundTrip(t *testing.T) {
	when := time.Date(2026, 6, 21, 12, 30, 45, 0, time.UTC)
	cases := []struct {
		name     string
		lat, lng float64
	}{
		{"San Francisco", 37.7749, -122.4194},
		{"Sydney", -33.8688, 151.2093},
		{"Tokyo", 35.6762, 139.6503},
		{"equator/prime", 0.0, 0.0},
		{"high north", 78.22, 15.65},
	}
	for _, c := range cases {
		rmc := BuildRMC(c.lat, c.lng, when, true)
		if !gps.ValidateChecksum(rmc) {
			t.Errorf("%s: RMC checksum invalid: %s", c.name, rmc)
		}
		parsed, err := gps.ParseRMC(rmc)
		if err != nil {
			t.Errorf("%s: ParseRMC: %v (%s)", c.name, err, rmc)
			continue
		}
		if !parsed.Valid {
			t.Errorf("%s: parsed RMC not valid", c.name)
		}
		if math.Abs(parsed.Latitude-c.lat) > 0.001 {
			t.Errorf("%s: latitude round-trip got %v want %v", c.name, parsed.Latitude, c.lat)
		}
		if math.Abs(parsed.Longitude-c.lng) > 0.001 {
			t.Errorf("%s: longitude round-trip got %v want %v", c.name, parsed.Longitude, c.lng)
		}

		gga := BuildGGA(c.lat, c.lng, when, 1)
		if !gps.ValidateChecksum(gga) {
			t.Errorf("%s: GGA checksum invalid: %s", c.name, gga)
		}
		pg, err := gps.ParseGGA(gga)
		if err != nil {
			t.Errorf("%s: ParseGGA: %v (%s)", c.name, err, gga)
			continue
		}
		if pg.FixQuality != 1 {
			t.Errorf("%s: GGA fix quality got %d want 1", c.name, pg.FixQuality)
		}
		if math.Abs(pg.Latitude-c.lat) > 0.001 || math.Abs(pg.Longitude-c.lng) > 0.001 {
			t.Errorf("%s: GGA position round-trip got (%v,%v) want (%v,%v)",
				c.name, pg.Latitude, pg.Longitude, c.lat, c.lng)
		}
	}
}

// An invalid status renders a V (void) fix that still parses.
func TestBuildRMCVoid(t *testing.T) {
	rmc := BuildRMC(10, 20, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false)
	if !gps.ValidateChecksum(rmc) {
		t.Fatalf("void RMC checksum invalid: %s", rmc)
	}
	parsed, err := gps.ParseRMC(rmc)
	if err != nil {
		t.Fatalf("ParseRMC: %v", err)
	}
	if parsed.Valid {
		t.Error("expected a void (invalid) fix")
	}
}
