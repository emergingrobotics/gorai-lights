package schedule

import (
	"fmt"
	"time"
)

// Window is one on/off interval for a light (REQ-CFG-5). Every window is
// midnight-to-midnight within a single local day and MUST NOT cross midnight
// (REQ-CFG-6): the resolved on time must be strictly before the resolved off
// time. Overnight coverage is expressed as two windows.
type Window struct {
	On  TimeSpec
	Off TimeSpec
}

// ValidateStatic performs the validation possible without the day's solar times
// (REQ-CFG-9, REQ-CFG-6). A clock→clock window is fully checked here: off must
// be strictly after on within the same day. Windows involving a solar event are
// only checked structurally now (both specs parsed) and fully validated when
// resolved against a concrete day's solar times (Resolve).
func (w Window) ValidateStatic() error {
	if w.On.IsSolar() || w.Off.IsSolar() {
		return nil
	}
	on := w.On.Hour*60 + w.On.Minute
	off := w.Off.Hour*60 + w.Off.Minute
	if off <= on {
		return fmt.Errorf("window off (%s) must be after on (%s); windows do not cross midnight", w.Off, w.On)
	}
	return nil
}

// Resolve turns the window into a concrete [on, off) interval on the given local
// day. day must be midnight of the target day in loc; sunrise/sunset are that
// day's solar events (or zero if unavailable). ok=false means a referenced solar
// event is unavailable and the window must be deferred (REQ-SOLAR-4). A resolved
// off <= on is a no-wrap violation (REQ-CFG-6) and returns an error.
func (w Window) Resolve(day time.Time, loc *time.Location, sunrise, sunset time.Time) (on, off time.Time, ok bool, err error) {
	on, onOK := w.On.Resolve(day, loc, sunrise, sunset)
	off, offOK := w.Off.Resolve(day, loc, sunrise, sunset)
	if !onOK || !offOK {
		return time.Time{}, time.Time{}, false, nil
	}
	if !off.After(on) {
		return time.Time{}, time.Time{}, false,
			fmt.Errorf("window off (%s=%s) must be after on (%s=%s); windows do not cross midnight",
				w.Off, off.Format(time.RFC3339), w.On, on.Format(time.RFC3339))
	}
	return on, off, true, nil
}
