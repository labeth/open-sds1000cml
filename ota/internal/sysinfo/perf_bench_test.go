// ENGMODEL-OWNER-UNIT: FU-OTA-SYSINFO
package sysinfo

import "testing"

// TRLC-LINKS: REQ-SDS-102
func BenchmarkUname(b *testing.B) {
	for b.Loop() {
		Uname()
	}
}

// TRLC-LINKS: REQ-SDS-102
func BenchmarkMem(b *testing.B) {
	for b.Loop() {
		mem()
	}
}
