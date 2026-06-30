package solar

import (
	"testing"
	"time"
)

// TEST-PLAN C1: a mid-latitude date returns sunrise before sunset, near the
// known San Francisco value for the summer solstice. Tolerance is generous
// because the algorithm is approximate and we only need ordering + ballpark.
func TestComputeMidLatitude(t *testing.T) {
	const sfLat, sfLng = 37.7749, -122.4194
	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	got := Compute(sfLat, sfLng, date)
	if got.Polar {
		t.Fatal("San Francisco in June must not be polar")
	}
	if !got.Sunrise.Before(got.Sunset) {
		t.Fatalf("sunrise %v must be before sunset %v", got.Sunrise, got.Sunset)
	}

	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// SF summer-solstice sunrise is roughly 05:48 local, sunset roughly 20:35.
	riseLocal := got.Sunrise.In(loc)
	setLocal := got.Sunset.In(loc)
	if riseLocal.Hour() < 4 || riseLocal.Hour() > 7 {
		t.Errorf("sunrise local hour %d out of expected range", riseLocal.Hour())
	}
	if setLocal.Hour() < 19 || setLocal.Hour() > 22 {
		t.Errorf("sunset local hour %d out of expected range", setLocal.Hour())
	}
}

// TEST-PLAN C1: a high-latitude midsummer date has no sunset (polar day) and is
// reported gracefully, not as an error (REQ-SOLAR-6).
func TestComputePolar(t *testing.T) {
	// Longyearbyen, Svalbard — continuous daylight in late June.
	const lat, lng = 78.22, 15.65
	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	got := Compute(lat, lng, date)
	if !got.Polar {
		t.Fatalf("expected polar (no sunrise/sunset) at 78N in June, got %+v", got)
	}
	if !got.Sunrise.IsZero() || !got.Sunset.IsZero() {
		t.Error("polar result must carry zero event times")
	}
}
