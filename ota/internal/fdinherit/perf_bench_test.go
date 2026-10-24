// ENGMODEL-OWNER-UNIT: FU-OTA-FDINHERIT
package fdinherit

import "testing"

// TRLC-LINKS: REQ-SDS-085
func BenchmarkHoldersOf(b *testing.B) {
	for b.Loop() {
		HoldersOf("/dev/null")
	}
}

// TRLC-LINKS: REQ-SDS-085
func BenchmarkDescendantsOfSelf(b *testing.B) {
	for b.Loop() {
		DescendantsOfSelf()
	}
}
