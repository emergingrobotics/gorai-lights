package schedule

import "time"

// DesiredState computes whether a light should be ON at the instant now, given
// its windows, the resolved timezone, and the day's solar times (REQ-SCHED-5).
//
// The light is ON when now falls inside any window's [on, off) interval for the
// local day (REQ-CFG-5, REQ-CFG-7: overlapping windows union). Windows whose
// solar event is unavailable are skipped and reported via deferred so the caller
// can log and avoid driving against unresolved times (REQ-SOLAR-4). A window
// that resolves to a no-wrap violation is also skipped and surfaced via err for
// the caller to log; it never forces the light on.
//
// now and the day boundary are interpreted in loc, so DST transitions are
// handled by the standard library (REQ-TIME-1).
func DesiredState(now time.Time, windows []Window, loc *time.Location, sunrise, sunset time.Time) (on bool, deferred bool, err error) {
	local := now.In(loc)
	y, mo, d := local.Date()
	day := time.Date(y, mo, d, 0, 0, 0, 0, loc)

	for _, w := range windows {
		onAt, offAt, ok, rerr := w.Resolve(day, loc, sunrise, sunset)
		if rerr != nil {
			// Remember the first resolution error but keep evaluating other
			// windows; a single bad window must not hide a valid one.
			if err == nil {
				err = rerr
			}
			continue
		}
		if !ok {
			deferred = true
			continue
		}
		// Half-open interval [on, off): on at the on edge, off at the off edge
		// (REQ-CFG-5, TEST-PLAN B3 boundaries).
		if !local.Before(onAt) && local.Before(offAt) {
			on = true
		}
	}
	return on, deferred, err
}
