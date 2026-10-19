// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"runtime"
	"testing"
)

// decodeAlloc is the bytes one decode of a busy line of n samples (a toggle
// every per samples on line A, a slower one on B) allocates.
// TRLC-LINKS: REQ-SDS-018
func decodeAlloc(d SerialDecoder, proto, n, per int) uint64 {
	a, b := make([]uint8, n), make([]uint8, n)
	for i := range a {
		if (i/per)&1 == 1 {
			a[i] = 255
		}
		if (i/(2*per+1))&1 == 1 {
			b[i] = 255
		}
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	d.Decode(a, b, 1e-6, SerialParams{Proto: proto, ChA: 0, ChB: 1, HaveThr: true, Threshold: 128})
	runtime.ReadMemStats(&m1)
	return m1.TotalAlloc - m0.TotalAlloc
}

// TestDecodersLinearOnBusyLine decodes a busy line (a toggle every few
// samples: a fast bus, noise) with every protocol's decoder at two record
// lengths: four times the record allocates about four times as much, not
// sixteen. A decoder that keeps a growing string per unit (Morse's letter
// code on a line that never pauses for a letter) costs quadratic time and
// gigabytes of garbage on such a line, on every record.
// TRLC-LINKS: REQ-SDS-018
func TestDecodersLinearOnBusyLine(t *testing.T) {
	for proto := 1; proto < SerialProtocols; proto++ {
		d, ok := serialDecoder(proto)
		if !ok {
			continue
		}
		for _, per := range []int{1, 2, 3, 9} {
			small, big := decodeAlloc(d, proto, 1<<15, per), decodeAlloc(d, proto, 1<<17, per)
			if big > 8*small+1<<20 {
				t.Errorf("%s on a line toggling every %d samples: %d KB for 32K samples, %d KB for 128K", d.Name, per, small>>10, big>>10)
			}
		}
	}
}
