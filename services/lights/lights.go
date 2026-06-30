// Package lights implements the gorai-lights scheduler service
// ("scheduler"/"lights"): it reads the robot's GPS position, derives the local
// timezone and daily sunrise/sunset, and publishes Tasmota on/off commands over
// NATS when each light's schedule windows say it should change (REQUIREMENTS
// §6, DESIGN §5).
//
// In NCP terms (see ../../../gorai/VISION.md):
//   - GPS is a resource — its NMEA stream is read on gorai.<ns>.<gps>.data for
//     latitude/longitude (REQ-SOLAR-2). The system clock provides wall time
//     (REQ-TIME-2); GPS supplies position, not the clock.
//   - each Tasmota light is a tool — invoked on gorai.<ns>.tasmota.<device>.command.
//
// The scheduler never performs device I/O (REQ-SCHED-3); it only publishes
// commands, which the Tasmota controller (or the test simulator) consumes. Any
// other component may publish the same command (REQ-ARCH-3).
package lights

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"
	"github.com/emergingrobotics/gorai/pkg/subjects"

	"github.com/emergingrobotics/gorai-gps/pkg/gps"

	"github.com/emergingrobotics/gorai-lights/internal/schedule"
	"github.com/emergingrobotics/gorai-lights/internal/solar"
	"github.com/emergingrobotics/gorai-lights/internal/tz"
)

func init() {
	registry.RegisterService("scheduler", "lights", New)
}

// evalInterval is how often the scheduler re-evaluates desired state to detect
// a crossed trigger (REQ-SCHED-1). It is independent of the (typically longer)
// reconcile re-assert cadence (REQ-SCHED-2).
const evalInterval = time.Second

// Scheduler publishes Tasmota commands from a GPS-located, timezone-aware daily
// schedule.
type Scheduler struct {
	logger         *slog.Logger
	nc             *nats.Conn
	cfg            *Config
	gpsDataSubject string

	mu      sync.RWMutex
	haveFix bool
	lat     float64
	lng     float64

	// The following are owned by the reconcile goroutine only (no locking).
	tzResolved   tz.Resolution
	tzFinal      bool
	solarDate    string
	solarTimes   solar.Times
	solarFromFix bool              // whether solarTimes were computed from a live fix
	lastState    map[string]string // light name -> last published "on"/"off" ("" = none yet)

	subs []*nats.Subscription
	stop chan struct{}
}

// New constructs the scheduler from its RDL attributes (DESIGN §5).
func New(ctx context.Context, deps registry.Dependencies, conf registry.Config) (any, error) {
	cfg, err := parseConfig(conf)
	if err != nil {
		return nil, fmt.Errorf("lights scheduler config: %w", err)
	}

	logger := slog.Default()
	var nc *nats.Conn
	if deps != nil {
		if v, derr := deps.Get("logger"); derr == nil {
			if l, ok := v.(*slog.Logger); ok {
				logger = l
			}
		}
		if v, derr := deps.Get("nats"); derr == nil {
			if c, ok := v.(*nats.Conn); ok {
				nc = c
			}
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("lights scheduler: NATS connection unavailable")
	}

	sb := subjects.NewBuilder(cfg.namespace)
	return &Scheduler{
		logger:         logger.With("service", "lights-scheduler"),
		nc:             nc,
		cfg:            cfg,
		gpsDataSubject: sb.ComponentData(cfg.gpsName),
		lastState:      make(map[string]string, len(cfg.lights)),
		stop:           make(chan struct{}),
	}, nil
}

// SubscribeGPS subscribes to the GPS resource and resolves an explicit timezone
// override (which is final and independent of any fix, REQ-TZ-2). It does NOT
// start the reconcile loop, so the test harness can receive injected GPS fixes
// and then drive the schedule deterministically with EvaluateOnce. Start calls
// it on the production path.
func (s *Scheduler) SubscribeGPS() error {
	if s.cfg.timezoneOverride != "" {
		s.tzResolved = tz.Resolve(s.cfg.timezoneOverride, 0, 0, false)
		s.tzFinal = !s.tzResolved.Degraded
	}
	sub, err := s.nc.Subscribe(s.gpsDataSubject, s.onGPS)
	if err != nil {
		return fmt.Errorf("subscribe GPS %q: %w", s.gpsDataSubject, err)
	}
	s.subs = append(s.subs, sub)
	return nil
}

// Start subscribes to the GPS resource and launches the reconcile loop.
func (s *Scheduler) Start(ctx context.Context) error {
	if err := s.SubscribeGPS(); err != nil {
		return err
	}
	go s.run(ctx)
	s.logger.Info("lights scheduler started",
		"gps", s.gpsDataSubject, "lights", len(s.cfg.lights),
		"reconcile", s.cfg.reconcileInterval, "timezone_override", s.cfg.timezoneOverride)
	return nil
}

// Close stops the reconcile loop and unsubscribes.
func (s *Scheduler) Close(ctx context.Context) error {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	return nil
}

// onGPS records the latest valid position from the GPS NMEA stream
// (REQ-SOLAR-2). Time-of-day is ignored: v3 uses the system clock (REQ-TIME-2).
func (s *Scheduler) onGPS(m *nats.Msg) {
	var msg gps.GPSMessage
	if err := json.Unmarshal(m.Data, &msg); err != nil {
		return
	}
	lat, lng, ok := parsePosition(msg.Sentence)
	if !ok {
		return
	}
	s.mu.Lock()
	s.lat, s.lng, s.haveFix = lat, lng, true
	s.mu.Unlock()
}

// EvaluateOnce runs a single scheduling evaluation at the supplied instant,
// publishing each light's desired state on change (or re-asserting when
// reassert is set). The production reconcile loop calls the same logic on the
// system clock; this exported entry point lets the test harness drive the
// schedule deterministically at a chosen instant (REQUIREMENTS §11).
//
// Precondition: do not call EvaluateOnce concurrently with a running reconcile
// loop. The evaluation state (timezone/solar caches, last-published map) is owned
// by a single evaluator; tests use SubscribeGPS (not Start) so no loop runs.
func (s *Scheduler) EvaluateOnce(now time.Time, reassert bool) {
	s.evaluate(now, reassert)
}

// Position returns the best available position: a live GPS fix, else the
// configured location fallback, else have=false (REQ-SOLAR-4).
func (s *Scheduler) Position() (lat, lng float64, have bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.haveFix {
		return s.lat, s.lng, true
	}
	if s.cfg.haveFallback {
		return s.cfg.fallbackLat, s.cfg.fallbackLng, true
	}
	return 0, 0, false
}

// haveLiveFix reports whether a real GPS fix (not the configured fallback) has
// been received.
func (s *Scheduler) haveLiveFix() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.haveFix
}

// run evaluates the schedule on evalInterval, publishing on state change and
// re-asserting on the reconcile cadence.
func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(evalInterval)
	defer ticker.Stop()

	lastReassert := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case now := <-ticker.C:
			reassert := lastReassert.IsZero() || now.Sub(lastReassert) >= s.cfg.reconcileInterval
			if reassert {
				lastReassert = now
			}
			s.evaluate(now, reassert)
		}
	}
}

// evaluate resolves timezone and solar for now, then publishes each light's
// desired state on change (or re-asserts when reassert is set).
func (s *Scheduler) evaluate(now time.Time, reassert bool) {
	loc := s.ensureTimezone()
	solarTimes := s.ensureSolar(loc, now)

	for _, lc := range s.cfg.lights {
		on, deferred, err := schedule.DesiredState(now, lc.windows, loc, solarTimes.Sunrise, solarTimes.Sunset)
		if err != nil {
			s.logger.Warn("window resolution error", "light", lc.name, "error", err)
		}
		// A deferred solar window may actually be active, so an off result here
		// is unknown rather than off. Driving the light off would contradict
		// REQ-SOLAR-4 ("deferred, not driven"); only an affirmative on (from a
		// resolved window) or a fully-known off is published.
		if !on && deferred {
			s.logger.Debug("desired state unknown: solar window deferred", "light", lc.name)
			continue
		}

		want := stateWord(on)
		prev := s.lastState[lc.name]
		switch {
		case prev != want:
			reason := "trigger"
			if prev == "" {
				reason = "startup"
			}
			s.publish(lc, want, reason)
			s.lastState[lc.name] = want
		case reassert:
			s.publish(lc, want, "reconcile")
		}
	}
}

// ensureTimezone resolves the IANA timezone once a definitive answer is known,
// caching the result. Until then it reports the degraded UTC fallback so HH:MM
// windows still function (REQ-TZ-2, REQ-TZ-6).
func (s *Scheduler) ensureTimezone() *time.Location {
	if s.tzFinal {
		return s.tzResolved.Location
	}
	lat, lng, have := s.Position()
	res := tz.Resolve(s.cfg.timezoneOverride, lat, lng, have)
	if res.Degraded != s.tzResolved.Degraded || res.Zone != s.tzResolved.Zone {
		s.logger.Info("timezone resolved", "zone", res.String(), "have_position", have)
	}
	s.tzResolved = res
	// A non-degraded resolution is final (REQ-TZ-4: resolve on first valid fix).
	if !res.Degraded {
		s.tzFinal = true
	}
	return res.Location
}

// ensureSolar returns today's solar times for the current location, recomputing
// at the local-day rollover and once a position is first available
// (REQ-SOLAR-3). With no position the zero Times defers solar windows.
func (s *Scheduler) ensureSolar(loc *time.Location, now time.Time) solar.Times {
	lat, lng, have := s.Position()
	if !have {
		return solar.Times{}
	}
	live := s.haveLiveFix()
	date := now.In(loc).Format("2006-01-02")
	// Recompute daily (date rollover) and once when a live fix first supersedes
	// the configured fallback position, so sunrise/sunset reflect the robot's
	// real location rather than the fallback (REQ-SOLAR-3).
	if date == s.solarDate && !(live && !s.solarFromFix) {
		return s.solarTimes
	}
	s.solarDate = date
	s.solarFromFix = live
	s.solarTimes = solar.Compute(lat, lng, now.In(loc))
	if s.solarTimes.Polar {
		s.logger.Info("no sunrise/sunset this day (polar); solar windows deferred", "date", date)
	} else {
		s.logger.Info("solar times computed", "date", date,
			"sunrise", s.solarTimes.Sunrise.In(loc).Format(time.RFC3339),
			"sunset", s.solarTimes.Sunset.In(loc).Format(time.RFC3339))
	}
	return s.solarTimes
}

// publish sends a fire-and-forget command to the light's Tasmota device subject
// (REQ-ARCH-4) and logs it (REQ-SCHED-4).
func (s *Scheduler) publish(lc lightConfig, state, reason string) {
	subject := fmt.Sprintf("gorai.%s.tasmota.%s.command", s.cfg.namespace, lc.device)
	payload, err := json.Marshal(map[string]string{"state": state})
	if err != nil {
		s.logger.Error("marshal light command", "error", err, "light", lc.name)
		return
	}
	if err := s.nc.Publish(subject, payload); err != nil {
		s.logger.Error("publish light command", "error", err, "subject", subject)
		return
	}
	s.logger.Info("light command sent",
		"light", lc.name, "device", lc.device, "state", state, "reason", reason, "subject", subject)
}

func stateWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// parsePosition extracts a valid latitude/longitude from a GPS NMEA sentence.
// $..RMC is preferred (it carries a validity flag); $..GGA is accepted when its
// fix quality is non-zero. Returns ok=false for invalid fixes or other sentence
// types.
func parsePosition(sentence string) (lat, lng float64, ok bool) {
	if rmc, err := gps.ParseRMC(sentence); err == nil {
		if rmc.Valid {
			return rmc.Latitude, rmc.Longitude, true
		}
		return 0, 0, false
	}
	if gga, err := gps.ParseGGA(sentence); err == nil {
		if gga.FixQuality > 0 {
			return gga.Latitude, gga.Longitude, true
		}
		return 0, 0, false
	}
	return 0, 0, false
}
