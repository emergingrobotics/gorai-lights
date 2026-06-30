package tasmotasim

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"
)

// fakeDeps is a minimal registry.Dependencies that only provides the NATS conn.
type fakeDeps struct{ nc *nats.Conn }

func (d fakeDeps) Get(name string) (any, error) {
	if name == "nats" {
		return d.nc, nil
	}
	return nil, fmt.Errorf("dependency %q not provided", name)
}
func (d fakeDeps) GetByType(string) ([]any, error) { return nil, nil }

func startNATS(t *testing.T) (*natsserver.Server, *nats.Conn) {
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
	return ns, nc
}

func newLight(t *testing.T, nc *nats.Conn) *Light {
	t.Helper()
	v, err := New(context.Background(), fakeDeps{nc: nc}, registry.Config{
		"name": "porch-light", "namespace": "lights", "device": "porch",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	light := v.(*Light)
	if err := light.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = light.Close(context.Background()) })
	return light
}

func waitState(t *testing.T, ch <-chan stateMessage, want string) stateMessage {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case s := <-ch:
			if s.State == want {
				return s
			}
		case <-deadline:
			t.Fatalf("timed out waiting for state %q", want)
		}
	}
}

// TestSimLightSwitchesOnCommand proves the fire-and-forget tool path: a command
// on ...tasmota.porch.command flips the light and is reflected on ...porch.state.
func TestSimLightSwitchesOnCommand(t *testing.T) {
	ns, nc := startNATS(t)
	defer ns.Shutdown()
	defer nc.Close()

	newLight(t, nc)

	states := make(chan stateMessage, 16)
	if _, err := nc.Subscribe("gorai.lights.tasmota.porch.state", func(m *nats.Msg) {
		var s stateMessage
		if json.Unmarshal(m.Data, &s) == nil {
			states <- s
		}
	}); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()

	if err := nc.Publish("gorai.lights.tasmota.porch.command", []byte(`{"state":"on"}`)); err != nil {
		t.Fatal(err)
	}
	got := waitState(t, states, "on")
	if !got.Simulated || got.Device != "porch" {
		t.Fatalf("unexpected state message: %+v", got)
	}

	if err := nc.Publish("gorai.lights.tasmota.porch.command", []byte(`{"state":"off"}`)); err != nil {
		t.Fatal(err)
	}
	waitState(t, states, "off")
}

// TestSimLightRequestReply proves the request/reply tool contract: a Request
// returns a structured {"ok":true,"state":...} acknowledgement.
func TestSimLightRequestReply(t *testing.T) {
	ns, nc := startNATS(t)
	defer ns.Shutdown()
	defer nc.Close()

	newLight(t, nc)
	_ = nc.Flush()

	msg, err := nc.Request("gorai.lights.tasmota.porch.command", []byte(`{"state":"off"}`), 3*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		t.Fatalf("unmarshal reply %q: %v", msg.Data, err)
	}
	if !resp.OK || resp.State != "off" {
		t.Fatalf("unexpected reply: %s", msg.Data)
	}
}
