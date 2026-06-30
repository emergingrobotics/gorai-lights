// Command gorai-lights is an example Gorai robot: it keeps accurate time from a
// GPS resource and turns a Tasmota light on/off at prescribed times or in
// response to a "turn light on" event.
//
// The import list below IS the robot manifest (the Caddy model): each blank
// import registers its capability via init(). gorai.Run() reads robot.json and
// brings the robot up.
package main

import (
	gorai "github.com/emergingrobotics/gorai/pkg/gorai"

	// GPS component — an NCP resource (sensor) that streams NMEA on
	// gorai.<namespace>.gps.data. Registers "gps"/"nmea".
	_ "github.com/emergingrobotics/gorai-gps/component/nmea"

	// Local lights scheduler — the agent that reads GPS time and calls the
	// light tool. Registers "scheduler"/"lights".
	_ "github.com/emergingrobotics/gorai-lights/services/lights"

	// Simulated Tasmota light for test mode (used by robot.test.json). Inert
	// unless the config references it. Registers "tasmota"/"sim".
	_ "github.com/emergingrobotics/gorai-lights/services/tasmotasim"
)

func main() {
	gorai.Run()
}
