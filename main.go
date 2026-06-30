// Command gorai-lights is an example Gorai robot: a single binary that reads its
// GPS position, derives the local timezone and daily sunrise/sunset, and drives
// a Tasmota light on a per-day schedule of clock and solar windows.
//
// The import list below IS the robot manifest (the Caddy model): each blank
// import registers its capability via init(). gorai.Run() reads the robot config
// and brings the robot up. The config selects which Tasmota driver to use: the
// real HTTP controller ("tasmota"/"controller", robot.json) or the in-process
// simulator ("tasmota"/"sim", robot.test.json).
package main

import (
	gorai "github.com/emergingrobotics/gorai/pkg/gorai"

	// GPS component — an NCP resource (sensor) that streams NMEA on
	// gorai.<namespace>.gps.data. Registers "gps"/"nmea".
	_ "github.com/emergingrobotics/gorai-gps/component/nmea"

	// Local lights scheduler — the agent that reads GPS position and publishes
	// light commands. Registers "scheduler"/"lights".
	_ "github.com/emergingrobotics/gorai-lights/services/lights"

	// Real Tasmota driver from the sibling capability node: subscribes to each
	// device's command subject and drives the bulb over HTTP. Used by robot.json.
	// Registers "tasmota"/"controller".
	_ "github.com/emergingrobotics/gorai-tasmota/service/controller"

	// Simulated Tasmota light for test mode (used by robot.test.json). Inert
	// unless the config references it. Registers "tasmota"/"sim".
	_ "github.com/emergingrobotics/gorai-lights/services/tasmotasim"
)

func main() {
	gorai.Run()
}
