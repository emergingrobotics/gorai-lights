// Package schedule holds the pure scheduling logic for gorai-lights: parsing
// time-specs, validating windows, and computing a light's desired on/off state
// for a given instant. It performs no I/O — no NATS, no GPS, no HTTP — so the
// whole decision path is unit-testable in isolation (REQUIREMENTS §6, DESIGN §5).
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kind distinguishes a wall-clock time-spec from a solar-event one.
type Kind int

const (
	// Clock is a fixed wall-clock time in the resolved timezone.
	Clock Kind = iota
	// Sunrise is today's sunrise at the robot's location, plus Offset.
	Sunrise
	// Sunset is today's sunset at the robot's location, plus Offset.
	Sunset
)

// endOfDayHour is the hour value that denotes the midnight boundary at the end
// of the local day. "24:00" is permitted only as a window's off boundary
// (REQ-CFG-8); Go's time.Date normalizes hour 24 to 00:00 of the next day.
const endOfDayHour = 24

// TimeSpec is one parsed schedule time (REQ-CFG-8): either a clock time
// (Hour:Minute) or a solar event (Sunrise/Sunset) with a signed Offset.
type TimeSpec struct {
	Kind   Kind
	Hour   int           // Clock only
	Minute int           // Clock only
	Offset time.Duration // Sunrise/Sunset only; signed
	raw    string
}

// String returns the original text the spec was parsed from.
func (t TimeSpec) String() string { return t.raw }

// IsSolar reports whether resolving this spec requires the day's solar times.
func (t TimeSpec) IsSolar() bool { return t.Kind == Sunrise || t.Kind == Sunset }

// ParseTimeSpec parses a time-spec per REQ-CFG-8:
//
//	"HH:MM"            wall-clock (24h); "24:00" allowed as an end-of-day boundary
//	"sunrise"/"sunset" today's solar event, zero offset
//	"sunrise±HH:MM"    solar event with a signed offset (e.g. "sunset-00:30")
func ParseTimeSpec(s string) (TimeSpec, error) {
	raw := s
	s = strings.TrimSpace(s)
	if s == "" {
		return TimeSpec{}, fmt.Errorf("empty time-spec")
	}

	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "sunrise"):
		return parseSolar(Sunrise, "sunrise", lower, raw)
	case strings.HasPrefix(lower, "sunset"):
		return parseSolar(Sunset, "sunset", lower, raw)
	default:
		return parseClock(s, raw)
	}
}

func parseClock(s, raw string) (TimeSpec, error) {
	h, m, err := parseHHMM(s)
	if err != nil {
		return TimeSpec{}, fmt.Errorf("time-spec %q: %w", raw, err)
	}
	// 24:00 is the sole legal representation of the end-of-day boundary; any
	// other hour-24 value (e.g. 24:30) is out of range.
	if h == endOfDayHour && m != 0 {
		return TimeSpec{}, fmt.Errorf("time-spec %q: 24:00 is the only valid 24-hour value", raw)
	}
	return TimeSpec{Kind: Clock, Hour: h, Minute: m, raw: raw}, nil
}

func parseSolar(kind Kind, word, lower, raw string) (TimeSpec, error) {
	rest := lower[len(word):]
	if rest == "" {
		return TimeSpec{Kind: kind, raw: raw}, nil
	}
	sign := rest[0]
	if sign != '+' && sign != '-' {
		return TimeSpec{}, fmt.Errorf("time-spec %q: solar offset must start with + or -", raw)
	}
	h, m, err := parseHHMM(rest[1:])
	if err != nil {
		return TimeSpec{}, fmt.Errorf("time-spec %q offset: %w", raw, err)
	}
	if h == endOfDayHour {
		return TimeSpec{}, fmt.Errorf("time-spec %q: offset hour out of range", raw)
	}
	offset := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
	if sign == '-' {
		offset = -offset
	}
	return TimeSpec{Kind: kind, Offset: offset, raw: raw}, nil
}

// parseHHMM parses a strict "HH:MM" with hour 0–24 and minute 0–59. Hour 24 is
// allowed here so callers can apply the end-of-day boundary rule.
func parseHHMM(s string) (hour, minute int, err error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	hour, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("bad hour in %q", s)
	}
	minute, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("bad minute in %q", s)
	}
	if hour < 0 || hour > endOfDayHour {
		return 0, 0, fmt.Errorf("hour out of range in %q", s)
	}
	if minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("minute out of range in %q", s)
	}
	return hour, minute, nil
}

// Resolve returns the absolute instant of this spec on the given local day.
// day must be midnight (00:00) of the target day in loc. For solar specs the
// caller supplies the day's sunrise/sunset (already absolute); if the relevant
// event is the zero time (no solar fix yet, or a polar no-event day), Resolve
// returns ok=false so the caller can defer the window (REQ-SOLAR-4, REQ-SOLAR-6).
func (t TimeSpec) Resolve(day time.Time, loc *time.Location, sunrise, sunset time.Time) (instant time.Time, ok bool) {
	switch t.Kind {
	case Clock:
		y, mo, d := day.Date()
		// Hour 24 normalizes to 00:00 of the next day — the end-of-day boundary.
		return time.Date(y, mo, d, t.Hour, t.Minute, 0, 0, loc), true
	case Sunrise:
		if sunrise.IsZero() {
			return time.Time{}, false
		}
		return sunrise.Add(t.Offset), true
	case Sunset:
		if sunset.IsZero() {
			return time.Time{}, false
		}
		return sunset.Add(t.Offset), true
	default:
		return time.Time{}, false
	}
}
