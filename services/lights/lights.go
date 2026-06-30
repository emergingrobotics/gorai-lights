// Package lights implements the gorai-lights scheduler: an NCP agent that reads
// accurate time from a GPS resource and drives a Tasmota light tool.
//
// In NCP terms (see ../../../gorai/VISION.md):
//   - the GPS is a resource — read its NMEA stream on gorai.<ns>.<gps>.data
//   - the Tasmota light is a tool — invoke it on gorai.<ns>.tasmota.<device>.command
//   - a "turn light on" event arrives on gorai.<ns>.<source>.event
//
// The light comes on/off at prescribed times (disciplined to GPS UTC) or in
// response to a turn-on event. More resources (sensors) and tools (e.g. a
// button that emits the turn-on event) can be added later without changing
// this service.
package lights

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"
	"github.com/emergingrobotics/gorai/pkg/subjects"

	"github.com/emergingrobotics/gorai-gps/pkg/gps"
)

func init() {
	registry.RegisterService("scheduler", "lights", New)
}

// Scheduler turns a light on/off on a daily schedule (GPS-timed) and on a
// turn-on event.
type Scheduler struct {
	logger *slog.Logger
	nc     *nats.Conn

	gpsDataSubject  string // resource we read time from
	lightCmdSubject string // tool we invoke
	onEventSubject  string // event we react to ("" if unconfigured)

	onTime  string // "HH:MM" UTC, "" disables
	offTime string // "HH:MM" UTC, "" disables

	mu        sync.RWMutex
	haveGPS   bool
	gpsOffset time.Duration  // gpsUTC - systemUTC
	lastFired map[string]string // action -> date already actioned (one fire/day)

	subs []*nats.Subscription
	stop chan struct{}
}

// New constructs the scheduler from its RDL attributes.
func New(ctx context.Context, deps registry.Dependencies, conf registry.Config) (any, error) {
	name := stringOr(conf["name"], "lights")

	logger := slog.Default()
	if deps != nil {
		if v, err := deps.Get("logger"); err == nil {
			if l, ok := v.(*slog.Logger); ok {
				logger = l
			}
		}
	}
	logger = logger.With("service", "lights-scheduler", "name", name)

	var nc *nats.Conn
	if deps != nil {
		if v, err := deps.Get("nats"); err == nil {
			if c, ok := v.(*nats.Conn); ok {
				nc = c
			}
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("lights scheduler %q: NATS connection unavailable", name)
	}

	device := stringOr(conf["light_device"], "")
	if device == "" {
		return nil, fmt.Errorf("lights scheduler %q: 'light_device' attribute is required", name)
	}

	namespace := stringOr(conf["namespace"], "gorai")
	sb := subjects.NewBuilder(namespace)

	s := &Scheduler{
		logger:          logger,
		nc:              nc,
		gpsDataSubject:  sb.ComponentData(stringOr(conf["gps"], "gps")),
		lightCmdSubject: fmt.Sprintf("gorai.%s.tasmota.%s.command", namespace, device),
		onTime:          stringOr(conf["on_time"], ""),
		offTime:         stringOr(conf["off_time"], ""),
		lastFired:       make(map[string]string),
		stop:            make(chan struct{}),
	}
	if source := stringOr(conf["on_event"], ""); source != "" {
		s.onEventSubject = sb.ComponentEvent(source)
	}
	return s, nil
}

// Start subscribes to the GPS resource and the turn-on event, then runs the
// one-second schedule loop. Satisfies the runtime's Startable interface.
func (s *Scheduler) Start(ctx context.Context) error {
	sub, err := s.nc.Subscribe(s.gpsDataSubject, s.onGPS)
	if err != nil {
		return fmt.Errorf("subscribe GPS %q: %w", s.gpsDataSubject, err)
	}
	s.subs = append(s.subs, sub)
	s.logger.Info("disciplining clock to GPS", "subject", s.gpsDataSubject)

	if s.onEventSubject != "" {
		esub, err := s.nc.Subscribe(s.onEventSubject, s.onTurnOnEvent)
		if err != nil {
			return fmt.Errorf("subscribe event %q: %w", s.onEventSubject, err)
		}
		s.subs = append(s.subs, esub)
		s.logger.Info("listening for turn-on events", "subject", s.onEventSubject)
	}

	go s.run(ctx)
	s.logger.Info("lights scheduler started",
		"on", s.onTime, "off", s.offTime, "light_tool", s.lightCmdSubject)
	return nil
}

// Close stops the schedule loop and unsubscribes. Satisfies Closeable.
func (s *Scheduler) Close(ctx context.Context) error {
	close(s.stop)
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	return nil
}

// onGPS updates the GPS-vs-system clock offset from each NMEA sentence.
func (s *Scheduler) onGPS(m *nats.Msg) {
	var msg gps.GPSMessage
	if err := json.Unmarshal(m.Data, &msg); err != nil {
		return
	}
	t, ok := parseGPSTime(msg.Sentence)
	if !ok {
		return
	}
	s.mu.Lock()
	s.gpsOffset = t.Sub(time.Now().UTC())
	s.haveGPS = true
	s.mu.Unlock()
}

// onTurnOnEvent turns the light on in response to any event on the configured
// event subject (e.g. a future button-press tool publishing here).
func (s *Scheduler) onTurnOnEvent(m *nats.Msg) {
	s.logger.Info("turn-on event received", "subject", m.Subject)
	s.setLight(true, "event")
}

// run evaluates the schedule once per second against GPS-disciplined time.
func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	warnedNoFix := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			now, ok := s.effectiveNow()
			if !ok {
				if !warnedNoFix {
					s.logger.Warn("no GPS fix yet; schedule paused until first fix")
					warnedNoFix = true
				}
				continue
			}
			warnedNoFix = false

			hhmm := now.Format("15:04")
			date := now.Format("2006-01-02")
			if s.onTime != "" && hhmm == s.onTime && s.fireOnce("on", date) {
				s.setLight(true, "schedule")
			}
			if s.offTime != "" && hhmm == s.offTime && s.fireOnce("off", date) {
				s.setLight(false, "schedule")
			}
		}
	}
}

// effectiveNow returns GPS-disciplined UTC time, or system UTC with ok=false
// until the first GPS fix arrives.
func (s *Scheduler) effectiveNow() (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.haveGPS {
		return time.Now().UTC(), false
	}
	return time.Now().UTC().Add(s.gpsOffset), true
}

// fireOnce returns true at most once per day for a given action.
func (s *Scheduler) fireOnce(action, date string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastFired[action] == date {
		return false
	}
	s.lastFired[action] = date
	return true
}

// setLight invokes the Tasmota light tool. The NCP tool contract is
// request/reply; we publish fire-and-forget here so the example does not block
// when no Tasmota capability node is attached. Payload mirrors the Tasmota
// device action vocabulary ({"state":"on"|"off"}).
func (s *Scheduler) setLight(on bool, reason string) {
	state := "off"
	if on {
		state = "on"
	}
	payload, err := json.Marshal(map[string]string{"state": state})
	if err != nil {
		s.logger.Error("marshal light command", "error", err)
		return
	}
	if err := s.nc.Publish(s.lightCmdSubject, payload); err != nil {
		s.logger.Error("publish light command", "error", err, "subject", s.lightCmdSubject)
		return
	}
	s.logger.Info("light command sent", "state", state, "reason", reason, "subject", s.lightCmdSubject)
}

// parseGPSTime extracts a UTC timestamp from a GPS NMEA sentence. $..RMC carries
// full UTC date and time; $..GGA carries time-of-day only, in which case today's
// UTC date is assumed. Returns ok=false for other sentence types or unparseable
// fields. The gorai-gps parser does not expose the time field, so we read it here.
func parseGPSTime(sentence string) (time.Time, bool) {
	if i := strings.LastIndex(sentence, "*"); i >= 0 {
		sentence = sentence[:i]
	}
	f := strings.Split(sentence, ",")
	if len(f) < 2 {
		return time.Time{}, false
	}
	switch {
	case strings.HasSuffix(f[0], "RMC"):
		if len(f) < 10 {
			return time.Time{}, false
		}
		hh, mm, ss, ok := parseHMS(f[1])
		if !ok {
			return time.Time{}, false
		}
		dd, mo, yy, ok := parseDMY(f[9])
		if !ok {
			return time.Time{}, false
		}
		return time.Date(2000+yy, time.Month(mo), dd, hh, mm, ss, 0, time.UTC), true
	case strings.HasSuffix(f[0], "GGA"):
		hh, mm, ss, ok := parseHMS(f[1])
		if !ok {
			return time.Time{}, false
		}
		now := time.Now().UTC()
		return time.Date(now.Year(), now.Month(), now.Day(), hh, mm, ss, 0, time.UTC), true
	}
	return time.Time{}, false
}

func parseHMS(s string) (h, m, sec int, ok bool) {
	if len(s) < 6 {
		return 0, 0, 0, false
	}
	h, e1 := strconv.Atoi(s[0:2])
	m, e2 := strconv.Atoi(s[2:4])
	sec, e3 := strconv.Atoi(s[4:6])
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false
	}
	return h, m, sec, true
}

func parseDMY(s string) (d, mo, y int, ok bool) {
	if len(s) < 6 {
		return 0, 0, 0, false
	}
	d, e1 := strconv.Atoi(s[0:2])
	mo, e2 := strconv.Atoi(s[2:4])
	y, e3 := strconv.Atoi(s[4:6])
	if e1 != nil || e2 != nil || e3 != nil || mo < 1 || mo > 12 {
		return 0, 0, 0, false
	}
	return d, mo, y, true
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
