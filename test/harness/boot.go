package harness

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/config"
	"github.com/emergingrobotics/gorai/pkg/robot"

	"github.com/emergingrobotics/gorai-lights/test/natslisten"

	// Blank imports register the services/components the fixtures reference, so
	// the runtime can construct them by type/model (REQ-TEST-2).
	_ "github.com/emergingrobotics/gorai-lights/services/lights"
	_ "github.com/emergingrobotics/gorai-lights/services/tasmotasim"
	_ "github.com/emergingrobotics/gorai-lights/test/gpsinject"
)

// BootedRobot is a robot started through the real gorai runtime, with a command
// recorder attached to its NATS bus (REQ-TEST-14).
type BootedRobot struct {
	Robot     *robot.Robot
	NC        *nats.Conn
	Recorder  *natslisten.Recorder
	Namespace string
}

// BootFixture loads a fixture, starts it through the gorai runtime (the real
// scheduler/lights and tasmota/sim services on embedded NATS), and attaches a
// command recorder. The robot and recorder are torn down via t.Cleanup
// (REQ-TEST-15).
func BootFixture(t *testing.T, fixture []byte) *BootedRobot {
	t.Helper()

	cfg, err := config.LoadFromBytes(fixture)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	ctx := context.Background()
	r, err := robot.New(ctx, cfg, robot.WithLogger(quietLogger()))
	if err != nil {
		t.Fatalf("robot.New: %v", err)
	}
	if err := r.Start(ctx); err != nil {
		t.Fatalf("robot.Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Stop(stopCtx)
	})

	namespace := cfg.GetEffectiveNamespace()
	nc, err := nats.Connect(cfg.NATS.URL)
	if err != nil {
		t.Fatalf("connect to robot NATS %q: %v", cfg.NATS.URL, err)
	}
	t.Cleanup(func() { nc.Close() })

	rec, closeRec, err := natslisten.NewRecorder(nc, namespace)
	if err != nil {
		t.Fatalf("recorder: %v", err)
	}
	t.Cleanup(func() { _ = closeRec() })

	return &BootedRobot{Robot: r, NC: nc, Recorder: rec, Namespace: namespace}
}
