// Package tz resolves the robot's IANA timezone from its GPS position using an
// embedded, offline coordinate→zone table (github.com/bradfitz/latlong), so the
// correct zone and DST rules are chosen automatically from where the robot
// physically is — no manual config and no internet (REQ-TZ-1, REQ-TZ-3).
package tz

import (
	"fmt"
	"time"

	"github.com/bradfitz/latlong"
)

// Lookup returns the IANA zone name for a coordinate, or ok=false when the
// position matches no zone (e.g. open ocean) (REQ-TZ-5).
func Lookup(latitude, longitude float64) (string, bool) {
	zone := latlong.LookupZoneName(latitude, longitude)
	if zone == "" {
		return "", false
	}
	return zone, true
}

// Resolution is the outcome of resolving a timezone: the loaded location, the
// IANA zone name chosen, and whether the result is a degraded fallback (no
// override and no usable GPS position) that should be logged (REQ-TZ-2).
type Resolution struct {
	Location *time.Location
	Zone     string
	Degraded bool
}

// Resolve applies the precedence of REQ-TZ-2:
//  1. an explicit override zone name (from attributes.timezone), else
//  2. the GPS-position lookup once a fix is valid (haveFix), else
//  3. UTC as a degraded fallback.
//
// An override or looked-up zone that fails time.LoadLocation falls through to
// the next source rather than failing the robot (REQ-TZ-5: never fail to start).
func Resolve(override string, latitude, longitude float64, haveFix bool) Resolution {
	if override != "" {
		if loc, err := time.LoadLocation(override); err == nil {
			return Resolution{Location: loc, Zone: override}
		}
	}
	if haveFix {
		if zone, ok := Lookup(latitude, longitude); ok {
			if loc, err := time.LoadLocation(zone); err == nil {
				return Resolution{Location: loc, Zone: zone}
			}
		}
	}
	return Resolution{Location: time.UTC, Zone: "UTC", Degraded: true}
}

// String renders a resolution for logging.
func (r Resolution) String() string {
	if r.Degraded {
		return fmt.Sprintf("%s (degraded fallback)", r.Zone)
	}
	return r.Zone
}
