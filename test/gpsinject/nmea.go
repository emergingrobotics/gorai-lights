package gpsinject

import (
	"fmt"
	"strings"
	"time"
)

// BuildRMC builds a $GPRMC sentence with a valid checksum encoding the given
// position and instant (REQ-TEST-5). valid sets the A/V status flag. The time
// fields are rendered in UTC, as NMEA requires.
func BuildRMC(latitude, longitude float64, when time.Time, valid bool) string {
	when = when.UTC()
	latVal, latHem := formatLatitude(latitude)
	lngVal, lngHem := formatLongitude(longitude)
	status := "A"
	if !valid {
		status = "V"
	}
	body := strings.Join([]string{
		"GPRMC",
		formatHHMMSS(when),
		status,
		latVal, latHem,
		lngVal, lngHem,
		"000.0", // speed over ground (knots)
		"000.0", // course over ground
		formatDDMMYY(when),
		"", "", // magnetic variation (value, direction)
	}, ",")
	return withChecksum(body)
}

// BuildGGA builds a $GPGGA sentence with a valid checksum. fixQuality is the GGA
// fix indicator (0 = no fix, 1 = GPS fix).
func BuildGGA(latitude, longitude float64, when time.Time, fixQuality int) string {
	when = when.UTC()
	latVal, latHem := formatLatitude(latitude)
	lngVal, lngHem := formatLongitude(longitude)
	body := strings.Join([]string{
		"GPGGA",
		formatHHMMSS(when),
		latVal, latHem,
		lngVal, lngHem,
		fmt.Sprintf("%d", fixQuality),
		"08",   // satellites used
		"0.9",  // HDOP
		"10.0", // altitude
		"M",    // altitude units
		"0.0",  // geoid separation
		"M",    // separation units
		"", "", // DGPS age, station id
	}, ",")
	return withChecksum(body)
}

// withChecksum wraps a sentence body as $BODY*HH where HH is the NMEA XOR
// checksum of every character between the $ and the *.
func withChecksum(body string) string {
	var sum byte
	for i := 0; i < len(body); i++ {
		sum ^= body[i]
	}
	return fmt.Sprintf("$%s*%02X", body, sum)
}

// formatLatitude renders a latitude as ddmm.mmmm plus the N/S hemisphere.
func formatLatitude(latitude float64) (value, hemisphere string) {
	hemisphere = "N"
	if latitude < 0 {
		hemisphere = "S"
		latitude = -latitude
	}
	degrees := int(latitude)
	minutes := (latitude - float64(degrees)) * 60.0
	return fmt.Sprintf("%02d%07.4f", degrees, minutes), hemisphere
}

// formatLongitude renders a longitude as dddmm.mmmm plus the E/W hemisphere.
func formatLongitude(longitude float64) (value, hemisphere string) {
	hemisphere = "E"
	if longitude < 0 {
		hemisphere = "W"
		longitude = -longitude
	}
	degrees := int(longitude)
	minutes := (longitude - float64(degrees)) * 60.0
	return fmt.Sprintf("%03d%07.4f", degrees, minutes), hemisphere
}

func formatHHMMSS(when time.Time) string {
	return fmt.Sprintf("%02d%02d%02d", when.Hour(), when.Minute(), when.Second())
}

func formatDDMMYY(when time.Time) string {
	return fmt.Sprintf("%02d%02d%02d", when.Day(), int(when.Month()), when.Year()%100)
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

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
