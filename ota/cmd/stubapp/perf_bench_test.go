// ENGMODEL-OWNER-UNIT: FU-OTA-STUBAPP
package main

import "testing"

// The stub finds its inherited descriptors at start.
// TRLC-LINKS: REQ-SDS-177
func BenchmarkFindInheritedFD(b *testing.B) {
	for b.Loop() {
		findInheritedFD("/dev/Gpmc")
	}
}
