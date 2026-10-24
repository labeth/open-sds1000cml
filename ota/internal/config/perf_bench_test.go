// ENGMODEL-OWNER-UNIT: FU-OTA-CONFIG
package config

import "testing"

// The stick's agent.env names the device and its OTA dir.
// TRLC-LINKS: REQ-SDS-088
func BenchmarkLoad(b *testing.B) {
	b.Setenv("OTA_DEVICE_ID", "sds-lab")
	b.Setenv("OTA_DIR", "/mnt/usb/ota")
	for b.Loop() {
		Load()
	}
}
