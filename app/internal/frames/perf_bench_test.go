// ENGMODEL-OWNER-UNIT: FU-APP-FRAMES
package frames

import (
	"testing"
	"time"
)

// WaitNext when a newer frame is already there (the LCD's case at the
// frame rate).
// TRLC-LINKS: REQ-SDS-020
func BenchmarkWaitNextFresh(b *testing.B) {
	fo := New()
	fo.has, fo.latest.Seq = true, 7
	for b.Loop() {
		if fo.WaitNext(6, time.Second) != 7 {
			b.Fatal("seq")
		}
	}
}
