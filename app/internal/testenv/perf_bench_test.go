// ENGMODEL-OWNER-UNIT: FU-APP-TESTENV
package testenv

import "testing"

// TRLC-LINKS: REQ-SDS-179
func BenchmarkNeedNode(b *testing.B) {
	for b.Loop() {
		NeedNode(b)
	}
}
