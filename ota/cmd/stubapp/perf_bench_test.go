// ENGMODEL-OWNER-UNIT: FU-OTA-STUBAPP
package main

import (
	"fmt"
	"testing"
)

// The stub finds its inherited descriptors at start.
// TRLC-LINKS: REQ-SDS-177
func BenchmarkFindInheritedFD(b *testing.B) {
	for b.Loop() {
		findInheritedFD("/dev/Gpmc")
	}
}

// The token the stub rewrites every 400 ms.
// TRLC-LINKS: REQ-SDS-177
func BenchmarkHealthToken(b *testing.B) {
	var buf []byte
	for b.Loop() {
		buf = healthToken(buf[:0], 123456, 1700000000123456789)
	}
}

// The token as fmt.Sprintf made it (the reference).
// TRLC-LINKS: REQ-SDS-177
func BenchmarkHealthTokenRef(b *testing.B) {
	var buf []byte
	for b.Loop() {
		buf = []byte(fmt.Sprintf("frames=%d ts=%d\n", 123456, int64(1700000000123456789)))
	}
	_ = buf
}
