// Package harness provides shared setup for the gorai-lights test harness
// (REQUIREMENTS §11.6, REQ-TEST-14): an embedded NATS server, the REAL
// scheduler/lights service wired through its registered constructor, plus an
// attached GPS injector and command recorder. It also offers a fixture boot
// through the actual gorai runtime (BootFixture) for end-to-end smoke coverage.
//
// Determinism (REQ-TEST-15): the scheduler's GPS subscription is enabled, but
// its system-clock reconcile loop is NOT started; tests inject a fix and then
// call Scheduler.EvaluateOnce at a chosen instant, so results never depend on
// wall-clock time.
package harness

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"

	"github.com/emergingrobotics/gorai-lights/services/lights"
	"github.com/emergingrobotics/gorai-lights/test/gpsinject"
	"github.com/emergingrobotics/gorai-lights/test/natslisten"
)

// deps is the minimal registry.Dependencies the scheduler needs.
type deps struct {
	nc     *nats.Conn
	logger *slog.Logger
}

func (d deps) Get(name string) (any, error) {
	switch name {
	case "nats":
		return d.nc, nil
	case "logger":
		return d.logger, nil
	}
	return nil, fmt.Errorf("dependency %q not provided", name)
}
func (d deps) GetByType(string) ([]any, error) { return nil, nil }

// Harness bundles a running embedded NATS bus, the scheduler under test, and the
// injector/recorder attached to it.
type Harness struct {
	NC        *nats.Conn
	Scheduler *lights.Scheduler
	Injector  *gpsinject.Injector
	Recorder  *natslisten.Recorder
	Namespace string
}

// quietLogger discards scheduler logs so test output stays readable.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startNATS starts an embedded NATS server on an ephemeral port and connects a
// client, registering cleanup with t.
func startNATS(t *testing.T) *nats.Conn {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { nc.Close(); ns.Shutdown() })
	return nc
}

// New boots an embedded NATS bus, the scheduler from schedulerAttrs (the
// service's "attributes" object), and an attached injector + recorder. gpsName
// is the GPS component name the scheduler reads (its "gps" attribute, default
// "gps"). The reconcile loop is not started; drive the schedule via
// Harness.Scheduler.EvaluateOnce.
func New(t *testing.T, namespace, gpsName string, schedulerAttrs registry.Config) *Harness {
	t.Helper()
	nc := startNATS(t)
	logger := quietLogger()

	conf := registry.Config{"namespace": namespace}
	for k, v := range schedulerAttrs {
		conf[k] = v
	}
	if _, ok := conf["gps"]; !ok {
		conf["gps"] = gpsName
	}

	v, err := lights.New(context.Background(), deps{nc: nc, logger: logger}, conf)
	if err != nil {
		t.Fatalf("scheduler New: %v", err)
	}
	sched := v.(*lights.Scheduler)
	if err := sched.SubscribeGPS(); err != nil {
		t.Fatalf("scheduler SubscribeGPS: %v", err)
	}
	t.Cleanup(func() { _ = sched.Close(context.Background()) })

	rec, closeRec, err := natslisten.NewRecorder(nc, namespace)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}
	t.Cleanup(func() { _ = closeRec() })

	inj := gpsinject.NewInjector(nc, namespace, gpsName, logger)

	return &Harness{NC: nc, Scheduler: sched, Injector: inj, Recorder: rec, Namespace: namespace}
}

// InjectAndSettle injects a fix and blocks until the scheduler has recorded the
// position (or a short timeout), so a following EvaluateOnce sees it. This makes
// the inject→subscribe→evaluate path deterministic despite NATS being async.
func (h *Harness) InjectAndSettle(t *testing.T, latitude, longitude float64, when time.Time) {
	t.Helper()
	if err := h.Injector.Inject(latitude, longitude, when); err != nil {
		t.Fatalf("inject: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, have := h.Scheduler.Position(); have {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("scheduler did not record injected position")
}
