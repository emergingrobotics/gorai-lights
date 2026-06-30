package lights

import (
	"fmt"
	"time"

	"github.com/emergingrobotics/gorai-lights/internal/schedule"
)

const (
	defaultNamespace = "gorai"
	defaultGPSName   = "gps"
	// reconcileDefault is the re-assert cadence when none is configured
	// (REQ-CFG example, REQ-SCHED-2).
	reconcileDefault = 60 * time.Second
)

// lightConfig is one scheduled light: its name, the Tasmota device it maps to
// (REQ-CFG-3), and its validated schedule windows.
type lightConfig struct {
	name    string
	device  string
	windows []schedule.Window
}

// Config is the scheduler's parsed, validated RDL configuration (REQUIREMENTS §4).
type Config struct {
	namespace         string
	gpsName           string
	timezoneOverride  string
	reconcileInterval time.Duration
	fallbackLat       float64
	fallbackLng       float64
	haveFallback      bool
	lights            []lightConfig
}

// parseConfig reads and validates the scheduler attributes (REQ-CFG-1..9). The
// gorai runtime may merge attributes flat into conf or nest them under
// "attributes"; both layouts are accepted. Validation is strict (REQ-CFG-9):
// any malformed light, window, timespec, or no-wrap violation is a named error.
func parseConfig(conf map[string]any) (*Config, error) {
	attrs := conf
	if nested, ok := conf["attributes"].(map[string]any); ok {
		attrs = nested
	}

	c := &Config{
		namespace:         defaultNamespace,
		gpsName:           defaultGPSName,
		reconcileInterval: reconcileDefault,
	}
	// namespace may arrive at the top level (set by the runtime) or in attrs.
	if ns := stringOr(conf["namespace"], ""); ns != "" {
		c.namespace = ns
	}
	if ns := stringOr(attrs["namespace"], ""); ns != "" {
		c.namespace = ns
	}
	if v := stringOr(attrs["gps"], ""); v != "" {
		c.gpsName = v
	}
	c.timezoneOverride = stringOr(attrs["timezone"], "")

	if v := stringOr(attrs["reconcile_interval"], ""); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("reconcile_interval %q: %w", v, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("reconcile_interval must be > 0, got %s", d)
		}
		c.reconcileInterval = d
	}

	if err := parseLocation(attrs, c); err != nil {
		return nil, err
	}

	lightsRaw, ok := attrs["lights"].([]any)
	if !ok || len(lightsRaw) == 0 {
		return nil, fmt.Errorf("at least one light is required in 'lights'")
	}
	seen := make(map[string]bool, len(lightsRaw))
	for i, lr := range lightsRaw {
		lc, err := parseLight(lr, i)
		if err != nil {
			return nil, err
		}
		if seen[lc.name] {
			return nil, fmt.Errorf("duplicate light name %q", lc.name)
		}
		seen[lc.name] = true
		c.lights = append(c.lights, lc)
	}
	return c, nil
}

func parseLocation(attrs map[string]any, c *Config) error {
	locRaw, ok := attrs["location"]
	if !ok {
		return nil
	}
	loc, ok := locRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("location must be an object with latitude and longitude")
	}
	lat, okLat := toFloat(loc["latitude"])
	lng, okLng := toFloat(loc["longitude"])
	if !okLat || !okLng {
		return fmt.Errorf("location requires numeric latitude and longitude")
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return fmt.Errorf("location out of range: latitude=%v longitude=%v", lat, lng)
	}
	c.fallbackLat, c.fallbackLng, c.haveFallback = lat, lng, true
	return nil
}

func parseLight(raw any, index int) (lightConfig, error) {
	lm, ok := raw.(map[string]any)
	if !ok {
		return lightConfig{}, fmt.Errorf("lights[%d] must be an object", index)
	}
	name := stringOr(lm["name"], "")
	if name == "" {
		return lightConfig{}, fmt.Errorf("lights[%d]: 'name' is required", index)
	}
	device := stringOr(lm["device"], name)

	schedRaw, ok := lm["schedule"].([]any)
	if !ok || len(schedRaw) == 0 {
		return lightConfig{}, fmt.Errorf("light %q: a non-empty 'schedule' is required", name)
	}
	windows := make([]schedule.Window, 0, len(schedRaw))
	for j, wr := range schedRaw {
		w, err := parseWindow(wr, name, j)
		if err != nil {
			return lightConfig{}, err
		}
		windows = append(windows, w)
	}
	return lightConfig{name: name, device: device, windows: windows}, nil
}

func parseWindow(raw any, lightName string, index int) (schedule.Window, error) {
	wm, ok := raw.(map[string]any)
	if !ok {
		return schedule.Window{}, fmt.Errorf("light %q schedule[%d] must be an object", lightName, index)
	}
	onStr := stringOr(wm["on"], "")
	offStr := stringOr(wm["off"], "")
	if onStr == "" || offStr == "" {
		return schedule.Window{}, fmt.Errorf("light %q schedule[%d]: 'on' and 'off' are required", lightName, index)
	}
	on, err := schedule.ParseTimeSpec(onStr)
	if err != nil {
		return schedule.Window{}, fmt.Errorf("light %q schedule[%d] on: %w", lightName, index, err)
	}
	off, err := schedule.ParseTimeSpec(offStr)
	if err != nil {
		return schedule.Window{}, fmt.Errorf("light %q schedule[%d] off: %w", lightName, index, err)
	}
	w := schedule.Window{On: on, Off: off}
	if err := w.ValidateStatic(); err != nil {
		return schedule.Window{}, fmt.Errorf("light %q schedule[%d]: %w", lightName, index, err)
	}
	return w, nil
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// toFloat accepts the numeric shapes JSON decoding can produce for a coordinate.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
