// Package gpsinject is a test-only gorai component that injects synthetic GPS
// data on the same NATS subject the real GPS component uses
// (gorai.<namespace>.<name>.data), so a robot under test can be placed at any
// latitude/longitude — the single seam used to exercise timezone derivation for
// every zone (REQUIREMENTS §11.3, REQ-TEST-4..7).
//
// It is never imported by the production binary; it registers the model
// ("gps","inject") only for test fixtures, and its Injector is also usable
// directly from tests without the runtime.
package gpsinject

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
)

func init() {
	registry.RegisterComponent("gps", "inject", New)
}

// Injector publishes synthetic GPS fixes on a component data subject. Time is
// always supplied by the caller (never the wall clock) so tests are
// deterministic (REQ-TEST-7).
type Injector struct {
	nc      *nats.Conn
	subject string
	logger  *slog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewInjector builds an injector that publishes on gorai.<namespace>.<gpsName>.data.
func NewInjector(nc *nats.Conn, namespace, gpsName string, logger *slog.Logger) *Injector {
	if logger == nil {
		logger = slog.Default()
	}
	sb := subjects.NewBuilder(namespace)
	return &Injector{
		nc:      nc,
		subject: sb.ComponentData(gpsName),
		logger:  logger.With("component", "gps-inject", "subject", sb.ComponentData(gpsName)),
	}
}

// Subject returns the data subject the injector publishes on.
func (i *Injector) Subject() string { return i.subject }

// Inject publishes a single valid fix (an RMC then a GGA sentence) for the given
// position and instant (REQ-TEST-5). when is encoded into the sentences but the
// gorai-lights scheduler ignores GPS time (REQ-TIME-2); it is included for wire
// fidelity and for any consumer that does read it.
func (i *Injector) Inject(latitude, longitude float64, when time.Time) error {
	for _, sentence := range []string{
		BuildRMC(latitude, longitude, when, true),
		BuildGGA(latitude, longitude, when, 1),
	} {
		msg := gps.GPSMessage{Sentence: sentence, Timestamp: when, Source: "gps-inject"}
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal gps message: %w", err)
		}
		if err := i.nc.Publish(i.subject, data); err != nil {
			return fmt.Errorf("publish gps message: %w", err)
		}
	}
	return i.nc.Flush()
}

// StartContinuous re-publishes the fix on every interval until the returned
// context is cancelled or StopContinuous is called, mimicking a live 1 Hz stream
// (REQ-TEST-7). when is held fixed so consumers that read GPS time stay
// deterministic.
func (i *Injector) StartContinuous(latitude, longitude float64, when time.Time, interval time.Duration) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		_ = i.Inject(latitude, longitude, when)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := i.Inject(latitude, longitude, when); err != nil {
					i.logger.Warn("inject failed", "error", err)
				}
			}
		}
	}()
}

// StopContinuous halts continuous injection.
func (i *Injector) StopContinuous() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.cancel != nil {
		i.cancel()
		i.cancel = nil
	}
}

// handles lets a fixture-booted injector be retrieved by instance name from a
// test after the runtime constructs it (REQ-TEST-4 fixture path).
var (
	handlesMu sync.Mutex
	handles   = map[string]*Injector{}
)

// Handle returns the injector created for the named component instance, or nil.
func Handle(name string) *Injector {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	return handles[name]
}

// component wraps an Injector as a gorai component for fixture use.
type component struct {
	inj  *Injector
	name string
	cont *continuousConfig
}

// New constructs the injector as a gorai component. Optional attributes
// initial_latitude/initial_longitude with continuous=true begin a live stream
// at Start; otherwise the test drives Inject explicitly via Handle.
func New(ctx context.Context, deps registry.Dependencies, conf registry.Config) (any, error) {
	name := stringOr(conf["name"], "gps")
	namespace := stringOr(conf["namespace"], "gorai")

	var nc *nats.Conn
	logger := slog.Default()
	if deps != nil {
		if v, err := deps.Get("nats"); err == nil {
			if c, ok := v.(*nats.Conn); ok {
				nc = c
			}
		}
		if v, err := deps.Get("logger"); err == nil {
			if l, ok := v.(*slog.Logger); ok {
				logger = l
			}
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("gps inject %q: NATS connection unavailable", name)
	}

	attrs := conf
	if nested, ok := conf["attributes"].(map[string]any); ok {
		attrs = nested
	}
	inj := NewInjector(nc, namespace, name, logger)

	handlesMu.Lock()
	handles[name] = inj
	handlesMu.Unlock()

	c := &component{inj: inj, name: name}
	c.applyContinuous(attrs)
	return c, nil
}

type continuousConfig struct {
	lat, lng float64
	when     time.Time
	enabled  bool
}

func (c *component) applyContinuous(attrs map[string]any) {
	lat, okLat := toFloat(attrs["initial_latitude"])
	lng, okLng := toFloat(attrs["initial_longitude"])
	enabled, _ := attrs["continuous"].(bool)
	if okLat && okLng && enabled {
		c.cont = &continuousConfig{lat: lat, lng: lng, when: time.Unix(0, 0).UTC(), enabled: true}
	}
}

// cont holds optional continuous-mode config parsed from attributes.
func (c *component) Start(ctx context.Context) error {
	if c.cont != nil && c.cont.enabled {
		c.inj.StartContinuous(c.cont.lat, c.cont.lng, c.cont.when, time.Second)
	}
	return nil
}

// Close stops any continuous stream and drops the handle.
func (c *component) Close(ctx context.Context) error {
	c.inj.StopContinuous()
	handlesMu.Lock()
	delete(handles, c.name)
	handlesMu.Unlock()
	return nil
}
