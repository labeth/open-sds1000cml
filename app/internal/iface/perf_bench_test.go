// ENGMODEL-OWNER-UNIT: FU-APP-IFACE
package iface

import "testing"

// TRLC-LINKS: REQ-SDS-129
func BenchmarkBySel(b *testing.B) {
	last := registers[len(registers)-1]
	for b.Loop() {
		if _, ok := BySel(last.Sel); !ok {
			b.Fatal("missing")
		}
		if _, ok := ByName(last.Name); !ok {
			b.Fatal("missing")
		}
	}
}
