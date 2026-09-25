// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import "testing"

// TRLC-LINKS: REQ-SDS-169
func TestSRAMHealthStorage(t *testing.T) {
	for _, p := range []string{"/dev/acq-app.health", "/usr/bin/siglent/usr/media/U-disk0/acq2/health"} {
		if !sramHealthPathAllowed(p) {
			t.Fatalf("rejected volatile/external path %s", p)
		}
	}
	for _, p := range []string{"/dev/Gpmc", "/dev/acq/health", "/usr/bin/siglent/health", "/dev/acq-../../etc/health", "/usr/bin/siglent/usr/media/U-disk0/../../health", "relative"} {
		if sramHealthPathAllowed(p) {
			t.Fatalf("accepted internal/unsafe health path %s", p)
		}
	}
}
