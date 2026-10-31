// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"math/rand"
	"testing"
)

// TestScanRuns checks the word-at-a-time run scans against a byte loop for
// every level, start and alignment, on codes that sit near the level.
// TRLC-LINKS: REQ-SDS-018
func TestScanRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	buf := make([]uint8, 200)
	for trial := 0; trial < 300; trial++ {
		center := rng.Intn(256)
		spread := 1 + rng.Intn(40)
		for i := range buf {
			buf[i] = uint8(min(255, max(0, center+rng.Intn(2*spread+1)-spread)))
		}
		off := rng.Intn(9)
		codes := buf[off : off+rng.Intn(len(buf)-off)]
		for k := -2; k <= 258; k++ {
			from := rng.Intn(len(codes) + 2)
			want := len(codes)
			for j := from; j < len(codes); j++ {
				if int(codes[j]) >= k {
					want = j
					break
				}
			}
			if from >= len(codes) {
				want = len(codes)
			}
			if got := scanAtLeast(codes, from, k); got != want {
				t.Fatalf("scanAtLeast(k %d, from %d) %d, want %d (%v)", k, from, got, want, codes)
			}
			want = len(codes)
			for j := from; j < len(codes); j++ {
				if int(codes[j]) <= k {
					want = j
					break
				}
			}
			if got := scanAtMost(codes, from, k); got != want {
				t.Fatalf("scanAtMost(k %d, from %d) %d, want %d (%v)", k, from, got, want, codes)
			}
		}
	}
}

// TestHeldBeyondRuns checks the run-by-run held count against the sample
// loop it replaced, at thresholds between, on and off the codes.
// TRLC-LINKS: REQ-SDS-018
func TestHeldBeyondRuns(t *testing.T) {
	ref := func(codes []uint8, thr float64) float64 {
		var held [2]int
		run, side := 0, -1
		for _, v := range codes {
			s := 0
			if float64(v) >= thr {
				s = 1
			}
			if s == side {
				run++
			} else {
				run, side = 1, s
			}
			if run == 3 {
				held[s] += 3
			} else if run > 3 {
				held[s]++
			}
		}
		return float64(min(held[0], held[1]))
	}
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 3000; trial++ {
		codes := make([]uint8, rng.Intn(300))
		lo, hi := rng.Intn(256), rng.Intn(256)
		for i := range codes {
			if rng.Intn(1+rng.Intn(8)) == 0 {
				lo, hi = hi, lo
			}
			codes[i] = uint8(min(255, max(0, lo+rng.Intn(7)-3)))
		}
		for _, thr := range []float64{float64(lo), float64(hi), (float64(lo) + float64(hi)) / 2, -3, 0, 255, 255.5, 300, rng.Float64() * 256} {
			if got, want := heldBeyond(codes, thr), ref(codes, thr); got != want {
				t.Fatalf("heldBeyond(thr %v) %v, want %v (%v)", thr, got, want, codes)
			}
		}
	}
}
