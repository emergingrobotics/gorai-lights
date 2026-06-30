package robots

import (
	"testing"
	"time"

	"github.com/emergingrobotics/gorai/pkg/config"

	"github.com/emergingrobotics/gorai-lights/test/gpsinject"
	"github.com/emergingrobotics/gorai-lights/test/harness"
)

// REQ-TEST-2/3: every fixture parses and validates, and declares the scheduler
// service plus a GPS component using the injector model.
func TestFixturesValidate(t *testing.T) {
	for _, name := range Names {
		data, err := Load(name)
		if err != nil {
			t.Fatalf("%s: load: %v", name, err)
		}
		cfg, err := config.LoadFromBytes(data)
		if err != nil {
			t.Fatalf("%s: validate: %v", name, err)
		}
		hasScheduler := false
		for _, svc := range cfg.Services {
			if svc.Type == "scheduler" && svc.Model == "lights" {
				hasScheduler = true
			}
		}
		if !hasScheduler {
			t.Errorf("%s: missing scheduler/lights service", name)
		}
		hasInjector := false
		for _, comp := range cfg.Components {
			if comp.Type == "gps" && comp.Model == "inject" {
				hasInjector = true
			}
		}
		if !hasInjector {
			t.Errorf("%s: missing gps/inject component", name)
		}
	}
}

// REQ-TEST-14 / TEST-PLAN E2: the smoke fixture boots through the real gorai
// runtime; an all-day window drives the simulated light ON, observed on the
// command subject, and the runtime-created GPS injector accepts a fix.
func TestSmokeFixtureBootsThroughRuntime(t *testing.T) {
	data, err := Load("smoke.json")
	if err != nil {
		t.Fatal(err)
	}
	booted := harness.BootFixture(t, data)

	if _, ok := booted.Recorder.WaitFor("porch", "on", 5*time.Second); !ok {
		t.Fatal("expected the simulated porch light to be driven ON by the booted robot")
	}

	// The runtime constructed the injector; driving it must not error
	// (exercises the gps/inject component path through the real runtime).
	inj := gpsinject.Handle("gps")
	if inj == nil {
		t.Fatal("gps injector handle not registered by the runtime")
	}
	if err := inj.Inject(37.7749, -122.4194, time.Unix(0, 0).UTC()); err != nil {
		t.Fatalf("inject through runtime-created component: %v", err)
	}
}
