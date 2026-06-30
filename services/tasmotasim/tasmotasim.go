// Package tasmotasim is a simulated Tasmota light capability node for running
// gorai-lights in test mode — no smart-plug hardware and no external
// gorai-tasmota service required.
//
// It plays the same role a real Tasmota capability node would on the mesh:
//   - exposes the light as an NCP tool on gorai.<ns>.tasmota.<device>.command
//   - publishes the light's state as an NCP resource on ...tasmota.<device>.state
//
// Instead of issuing an HTTP call to a bulb, it just records the on/off state
// and logs every transition, so the example is observable end-to-end.
package tasmotasim

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/emergingrobotics/gorai/pkg/registry"
)

func init() {
	registry.RegisterService("tasmota", "sim", New)
}

type command struct {
	State string `json:"state"`
}

type stateMessage struct {
	Device    string `json:"device"`
	State     string `json:"state"`
	Simulated bool   `json:"simulated"`
	Timestamp string `json:"timestamp"`
}

// Light is a simulated Tasmota light.
type Light struct {
	logger       *slog.Logger
	nc           *nats.Conn
	device       string
	cmdSubject   string
	stateSubject string

	mu  sync.RWMutex
	on  bool
	sub *nats.Subscription
}

// New constructs the simulated light from its RDL attributes.
func New(ctx context.Context, deps registry.Dependencies, conf registry.Config) (any, error) {
	name := stringOr(conf["name"], "tasmota-sim")

	logger := slog.Default()
	if deps != nil {
		if v, err := deps.Get("logger"); err == nil {
			if l, ok := v.(*slog.Logger); ok {
				logger = l
			}
		}
	}

	var nc *nats.Conn
	if deps != nil {
		if v, err := deps.Get("nats"); err == nil {
			if c, ok := v.(*nats.Conn); ok {
				nc = c
			}
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("tasmota sim %q: NATS connection unavailable", name)
	}

	device := stringOr(conf["device"], "")
	if device == "" {
		return nil, fmt.Errorf("tasmota sim %q: 'device' attribute is required", name)
	}
	namespace := stringOr(conf["namespace"], "gorai")

	return &Light{
		logger:       logger.With("service", "tasmota-sim", "device", device),
		nc:           nc,
		device:       device,
		cmdSubject:   fmt.Sprintf("gorai.%s.tasmota.%s.command", namespace, device),
		stateSubject: fmt.Sprintf("gorai.%s.tasmota.%s.state", namespace, device),
	}, nil
}

// Start subscribes to the light's command tool and publishes initial state.
func (l *Light) Start(ctx context.Context) error {
	sub, err := l.nc.Subscribe(l.cmdSubject, l.onCommand)
	if err != nil {
		return fmt.Errorf("subscribe %q: %w", l.cmdSubject, err)
	}
	l.sub = sub
	l.logger.Info("simulated Tasmota light ready", "tool", l.cmdSubject, "state", l.stateSubject)
	l.publishState()
	return nil
}

// Close unsubscribes the command tool.
func (l *Light) Close(ctx context.Context) error {
	if l.sub != nil {
		return l.sub.Unsubscribe()
	}
	return nil
}

func (l *Light) onCommand(m *nats.Msg) {
	var cmd command
	if err := json.Unmarshal(m.Data, &cmd); err != nil {
		l.logger.Warn("ignoring malformed command", "error", err)
		return
	}

	on, ok := parseState(cmd.State)
	if !ok {
		l.logger.Warn("ignoring command with unknown state", "state", cmd.State)
		l.reply(m, `{"ok":false,"error":"unknown state"}`)
		return
	}

	l.mu.Lock()
	changed := l.on != on
	l.on = on
	l.mu.Unlock()

	word := stateWord(on)
	if changed {
		l.logger.Info("light switched", "state", word)
	} else {
		l.logger.Debug("light command (no change)", "state", word)
	}

	l.publishState()
	l.reply(m, fmt.Sprintf(`{"ok":true,"state":%q}`, word))
}

func (l *Light) publishState() {
	l.mu.RLock()
	word := stateWord(l.on)
	l.mu.RUnlock()

	data, err := json.Marshal(stateMessage{
		Device:    l.device,
		State:     word,
		Simulated: true,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	if err := l.nc.Publish(l.stateSubject, data); err != nil {
		l.logger.Error("publish state", "error", err)
	}
}

// reply answers a request/reply tool call; a no-op for fire-and-forget calls.
func (l *Light) reply(m *nats.Msg, body string) {
	if m.Reply != "" {
		_ = m.Respond([]byte(body))
	}
}

func parseState(s string) (on, ok bool) {
	switch s {
	case "on", "ON", "true", "1":
		return true, true
	case "off", "OFF", "false", "0":
		return false, true
	}
	return false, false
}

func stateWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
