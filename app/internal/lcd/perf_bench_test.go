// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "testing"

// TRLC-LINKS: REQ-SDS-021
func BenchmarkDrawText(b *testing.B) {
	sf := NewMemSurface()
	for b.Loop() {
		DrawText(sf, 10, 100, "Time/div 200us  Trigger C1 Rise 1.65V", 0xffff, 1)
		DrawText(sf, 10, 200, "AUTO: UART 115200 8N1", 0xffff, 2)
		DrawText(sf, 790, 470, "clip", 0xffff, 2) // off the edge: clipped
	}
}
