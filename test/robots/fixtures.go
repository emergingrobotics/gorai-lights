// Package robots holds robot.json fixtures for the gorai-lights test harness
// (REQUIREMENTS §11.2, REQ-TEST-2/3). Each fixture boots the real
// scheduler/lights and tasmota/sim services on embedded NATS with the GPS
// component replaced by the test injector ("gps","inject"), so scenarios run
// with no hardware.
package robots

import "embed"

//go:embed *.json
var fixtures embed.FS

// Names lists the fixture files in this package.
var Names = []string{
	"clock.json",
	"solar.json",
	"multiwindow.json",
	"tzoverride.json",
	"nofix.json",
	"smoke.json",
}

// Load returns the bytes of a named fixture (e.g. "smoke.json").
func Load(name string) ([]byte, error) {
	return fixtures.ReadFile(name)
}
