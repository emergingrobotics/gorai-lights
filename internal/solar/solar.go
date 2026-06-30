// Package solar computes sunrise and sunset locally, in-process, from a
// latitude/longitude and date — no network, no external service (REQ-SOLAR-1).
// It wraps the pure-Go github.com/nathan-osman/go-sunrise algorithm so the robot
// works fully offline.
package solar

import (
	"time"

	gosunrise "github.com/nathan-osman/go-sunrise"
)

// Times holds a day's solar events. Sunrise and Sunset are absolute UTC instants.
// Polar is true when the sun neither rises nor sets that day (high latitudes),
// in which case Sunrise and Sunset are the zero time and callers defer solar
// windows (REQ-SOLAR-6).
type Times struct {
	Sunrise time.Time
	Sunset  time.Time
	Polar   bool
}

// Compute returns the sunrise/sunset for the given coordinates on the calendar
// day of date (interpreted by its year/month/day fields). go-sunrise returns the
// zero time for both events when there is no sunrise/sunset that day; Compute
// reports that as Polar.
func Compute(latitude, longitude float64, date time.Time) Times {
	year, month, day := date.Date()
	rise, set := gosunrise.SunriseSunset(latitude, longitude, year, month, day)
	if rise.IsZero() || set.IsZero() {
		return Times{Polar: true}
	}
	return Times{Sunrise: rise, Sunset: set}
}
