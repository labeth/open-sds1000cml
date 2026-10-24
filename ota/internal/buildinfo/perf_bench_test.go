// ENGMODEL-OWNER-UNIT: FU-OTA-BUILDINFO
package buildinfo

import "testing"

var sink string

// Every status, discover and online event names the version.
// TRLC-LINKS: REQ-SDS-178
func BenchmarkString(b *testing.B) {
	for b.Loop() {
		sink = String()
	}
}
