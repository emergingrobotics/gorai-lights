package timezones

// Embed the IANA timezone database into the test binary so the every-timezone
// matrix resolves zones identically on any host, including minimal CI images
// without /usr/share/zoneinfo (REQ-TEST-15: CI-safe).
import _ "time/tzdata"
